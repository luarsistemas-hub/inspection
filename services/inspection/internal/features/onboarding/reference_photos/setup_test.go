package reference_photos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/onboarding/coordinator"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type sessionStub struct{ session onboardingsession.Submission }

func (s sessionStub) LoadSubmission(_ context.Context, locator, csrf string) (onboardingsession.Submission, error) {
	if locator != "verified-session" || csrf != "csrf-proof" {
		return onboardingsession.Submission{}, errors.New("invalid session")
	}
	return s.session, nil
}

type storeStub struct {
	putCount int
	putErr   error
}

func (*storeStub) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (*storeStub) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (*storeStub) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (*storeStub) AbortMultipart(context.Context, string, string, string) error { return nil }
func (*storeStub) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (*storeStub) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (s *storeStub) Put(_ context.Context, _, _ string, _ io.Reader, _ int64, _ string) error {
	s.putCount++
	return s.putErr
}
func (*storeStub) Delete(context.Context, string, string) error { return nil }

func TestSetupRequiresDependencies(t *testing.T) {
	if err := Setup(http.NewServeMux(), Dependencies{}); err == nil {
		t.Fatal("missing upload dependencies accepted")
	}
}

func TestPrivateUploadConfirmsActualPhotoAndQueuesScreening(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.MediaObject{}, &database.OutboxIntent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, sessionID := identity.NewID(), identity.NewID()
	store := &storeStub{}
	d := Dependencies{
		DB: db, Store: objectstore.Store{Bucket: "private", Client: store},
		Sessions: sessionStub{session: onboardingsession.Submission{Session: onboardingsession.Session{ID: sessionID, TenantID: &tenantID, State: coordinator.StatePropertySaved, CurrentStep: onboardingsession.StepProperty}}},
		Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return db.WithContext(ctx).Transaction(fn)
		},
	}
	mux := http.NewServeMux()
	if err := Setup(mux, d); err != nil {
		t.Fatal(err)
	}
	imageBytes := testJPEG(t)
	key := identity.NewID().String()
	send := func(contentType, attentionItems string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="file"; filename="quarto.jpg"`)
		header.Set("Content-Type", contentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatal(err)
		}
		_ = writer.WriteField("description", "Quarto")
		_ = writer.WriteField("attentionItems", attentionItems)
		_ = writer.WriteField("clientMutationId", key)
		_ = writer.Close()
		req := httptest.NewRequest(http.MethodPost, path, &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: "inspection_onboarding", Value: "verified-session"})
		req.Header.Set("X-CSRF-Token", "csrf-proof")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		return response
	}
	if response := send("image/jpeg", `[" Cafeteira ","cafeteira","","Torneira"]`); response.Code != http.StatusOK {
		t.Fatalf("photo upload = %d: %s", response.Code, response.Body.String())
	} else {
		var result map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result["mediaId"] == "" {
			t.Fatalf("photo was not confirmed: %v %v", result, err)
		}
	}
	if response := send("image/jpeg", `["Cafeteira","Torneira"]`); response.Code != http.StatusOK || store.putCount != 1 {
		t.Fatalf("idempotent upload = %d, writes=%d", response.Code, store.putCount)
	}
	if response := send("image/jpeg", `["Geladeira"]`); response.Code != http.StatusConflict {
		t.Fatalf("changed attention items = %d", response.Code)
	}
	var media []database.MediaObject
	var intents []database.OutboxIntent
	if err := db.Find(&media).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Find(&intents).Error; err != nil {
		t.Fatal(err)
	}
	if len(media) != 1 || media[0].Status != "VERIFIED" || media[0].Description != "Quarto" || string(media[0].AttentionItems) != `["Cafeteira","Torneira"]` || len(intents) != 1 || intents[0].Type != "media.verified.v1" {
		t.Fatalf("upload was not saved for screening: %+v %+v", media, intents)
	}
	store.putErr = errors.New("store unavailable")
	key = identity.NewID().String()
	if response := send("image/jpeg", `[]`); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("dependency failure = %d", response.Code)
	}
	if response := send("application/octet-stream", `[]`); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid image type = %d", response.Code)
	}
}

func TestNormalizeAttentionItems(t *testing.T) {
	items, err := normalizeAttentionItems(`[" Cafeteira ","cafeteira","","Torneira"]`)
	if err != nil || string(items) != `["Cafeteira","Torneira"]` {
		t.Fatalf("items=%s err=%v", items, err)
	}
	if _, err := normalizeAttentionItems(`{"item":"Cafeteira"}`); err == nil {
		t.Fatal("object accepted as attention items")
	}
	tooLong := `[` + `"` + strings.Repeat("a", maxAttentionItemRunes+1) + `"` + `]`
	if _, err := normalizeAttentionItems(tooLong); err == nil {
		t.Fatal("long attention item accepted")
	}
	tooMany := make([]string, maxAttentionItems+1)
	for index := range tooMany {
		tooMany[index] = "item-" + string(rune('a'+index))
	}
	encoded, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeAttentionItems(string(encoded)); err == nil {
		t.Fatal("too many attention items accepted")
	}
}

func testJPEG(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := jpeg.Encode(&body, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}
