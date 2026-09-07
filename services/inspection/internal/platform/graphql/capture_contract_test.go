package graphql

import (
	"os"
	"strings"
	"testing"
)

func TestOriginQueryContractsIT411ToIT412(t *testing.T) {
	schema := captureSchema(t)
	for _, contract := range []string{"originVersions(assetId: ID!", "OriginVersionConnection", "pageInfo: PageInfo!"} {
		if !strings.Contains(schema, contract) {
			t.Fatalf("origin query contract is missing %q", contract)
		}
	}
}

func TestTask5MutationContractsIT473ToIT478IT509ToIT526(t *testing.T) {
	schema := captureSchema(t)
	for _, field := range []string{"inviteOriginCapture", "activateOriginVersion", "invalidateOriginVersion", "acceptProcessing", "createMediaUpload", "presignMediaParts", "completeMediaUpload", "saveCaptureMetadata", "declareCaptureImpossibility", "submitCapture", "requestRecapture", "submitRecapture", "declareSensitiveDetectionFalsePositive"} {
		if !strings.Contains(schema, field+"(") {
			t.Fatalf("mutation %s is absent", field)
		}
	}
	for _, contract := range []string{"externalCapture: ExternalCapture!", "responsibilityId: ID!", "templateVersionId: ID!", "reference: JSON!", "policy: JSON!", "confirmationOnly: Boolean!", "userErrors: [UserError!]!", "clientMutationId: String!"} {
		if !strings.Contains(schema, contract) {
			t.Fatalf("capture contract is missing %q", contract)
		}
	}
	resolver, err := os.ReadFile("resolvers/schema.resolvers.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"OriginVersions", "InviteOriginCapture", "ActivateOriginVersion", "InvalidateOriginVersion", "CreateMediaUpload", "PresignMediaParts", "CompleteMediaUpload", "SaveCaptureMetadata", "DeclareCaptureImpossibility", "SubmitCapture", "RequestRecapture", "SubmitRecapture", "DeclareSensitiveDetectionFalsePositive", "ExternalCapture"} {
		if strings.Contains(string(resolver), "not implemented: "+field) {
			t.Fatalf("resolver %s is not implemented", field)
		}
	}
}

func captureSchema(t *testing.T) string {
	t.Helper()
	payload, err := os.ReadFile("../../../schema.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
