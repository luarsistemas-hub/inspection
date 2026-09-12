package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"inspection/libs/identity"
	assignroles "inspection/services/inspection/internal/features/access/assign_role_scope"
	disablemembership "inspection/services/inspection/internal/features/access/disable_membership"
	explaineffectiveaccess "inspection/services/inspection/internal/features/access/explain_effective_access"
	inviteinternal "inspection/services/inspection/internal/features/access/invite_internal_user"
	listidentitymemberships "inspection/services/inspection/internal/features/access/list_identity_memberships"
	archiveasset "inspection/services/inspection/internal/features/assets/archive_asset"
	getasset "inspection/services/inspection/internal/features/assets/get_asset"
	listassets "inspection/services/inspection/internal/features/assets/list_assets"
	registerasset "inspection/services/inspection/internal/features/assets/register_asset"
	updateasset "inspection/services/inspection/internal/features/assets/update_asset"
	listaudit "inspection/services/inspection/internal/features/audit/list_events"
	recordaudit "inspection/services/inspection/internal/features/audit/record_event"
	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturedeclare "inspection/services/inspection/internal/features/capture/declare_impossibility"
	capturefinalize "inspection/services/inspection/internal/features/capture/finalize_submission"
	capturesave "inspection/services/inspection/internal/features/capture/save_metadata"
	capturesubmit "inspection/services/inspection/internal/features/capture/submit"
	inspectioncancel "inspection/services/inspection/internal/features/inspections/cancel"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	inspectionmanual "inspection/services/inspection/internal/features/inspections/create_manual"
	inspectionoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	inspectionget "inspection/services/inspection/internal/features/inspections/get"
	inspectioninvalidate "inspection/services/inspection/internal/features/inspections/invalidate"
	inspectionlist "inspection/services/inspection/internal/features/inspections/list"
	acceptprocessing "inspection/services/inspection/internal/features/invitations/accept_processing"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	requestotp "inspection/services/inspection/internal/features/invitations/request_otp"
	revokeinvitation "inspection/services/inspection/internal/features/invitations/revoke_invitation"
	verifyotp "inspection/services/inspection/internal/features/invitations/verify_otp"
	mediacomplete "inspection/services/inspection/internal/features/media/complete_upload"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	mediafalsepositive "inspection/services/inspection/internal/features/media/declare_false_positive"
	mediapresign "inspection/services/inspection/internal/features/media/presign_parts"
	mediareconcile "inspection/services/inspection/internal/features/media/reconcile_upload"
	receivemetastatus "inspection/services/inspection/internal/features/notifications/receive_meta_status"
	receivetwiliostatus "inspection/services/inspection/internal/features/notifications/receive_twilio_status"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	adminactivation "inspection/services/inspection/internal/features/onboarding/admin_activation"
	onboardingbootstrap "inspection/services/inspection/internal/features/onboarding/onboarding_bootstrap"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	originactivate "inspection/services/inspection/internal/features/origins/activate_version"
	origincore "inspection/services/inspection/internal/features/origins/core"
	origininvalidate "inspection/services/inspection/internal/features/origins/invalidate_version"
	origininvite "inspection/services/inspection/internal/features/origins/invite_capture"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	originresolve "inspection/services/inspection/internal/features/origins/resolve_reference"
	deactivateparticipant "inspection/services/inspection/internal/features/participants/deactivate_participant"
	getparticipant "inspection/services/inspection/internal/features/participants/get_participant"
	listparticipants "inspection/services/inspection/internal/features/participants/list_participants"
	setchannels "inspection/services/inspection/internal/features/participants/set_delivery_channels"
	upsertparticipant "inspection/services/inspection/internal/features/participants/upsert_participant"
	verifychannel "inspection/services/inspection/internal/features/participants/verify_channel"
	projectadd "inspection/services/inspection/internal/features/projects/add_exceptional_stage"
	projectclose "inspection/services/inspection/internal/features/projects/close_project"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	projectcreate "inspection/services/inspection/internal/features/projects/create_project"
	projectget "inspection/services/inspection/internal/features/projects/get_project"
	projectlist "inspection/services/inspection/internal/features/projects/list_projects"
	projectreopen "inspection/services/inspection/internal/features/projects/reopen_project"
	projectskip "inspection/services/inspection/internal/features/projects/skip_stage"
	projectstart "inspection/services/inspection/internal/features/projects/start_stage"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	recaptureexpire "inspection/services/inspection/internal/features/recapture/expire_request"
	recapturerequest "inspection/services/inspection/internal/features/recapture/request"
	recapturesubmit "inspection/services/inspection/internal/features/recapture/submit"
	publication "inspection/services/inspection/internal/features/reports/publication"
	schedulecancel "inspection/services/inspection/internal/features/schedules/cancel_schedule"
	schedulecore "inspection/services/inspection/internal/features/schedules/core"
	schedulecreate "inspection/services/inspection/internal/features/schedules/create_schedule"
	schedulelist "inspection/services/inspection/internal/features/schedules/list_schedules"
	scheduleupdate "inspection/services/inspection/internal/features/schedules/update_schedule"
	activatedefinition "inspection/services/inspection/internal/features/segments/activate_definition"
	listdefinitions "inspection/services/inspection/internal/features/segments/list_definitions"
	publishdefinition "inspection/services/inspection/internal/features/segments/publish_definition"
	resolvedefinition "inspection/services/inspection/internal/features/segments/resolve_definition"
	activatetemplate "inspection/services/inspection/internal/features/templates/activate_template"
	listtemplates "inspection/services/inspection/internal/features/templates/list_templates"
	publishprofile "inspection/services/inspection/internal/features/templates/publish_analysis_profile"
	publishseeds "inspection/services/inspection/internal/features/templates/publish_seeds"
	publishtemplate "inspection/services/inspection/internal/features/templates/publish_template"
	resolvetemplate "inspection/services/inspection/internal/features/templates/resolve_template"
	archiveunit "inspection/services/inspection/internal/features/tenancy/archive_business_unit"
	createunit "inspection/services/inspection/internal/features/tenancy/create_business_unit"
	createtenant "inspection/services/inspection/internal/features/tenancy/create_tenant"
	updatetenant "inspection/services/inspection/internal/features/tenancy/update_tenant"
	upsertunit "inspection/services/inspection/internal/features/tenancy/upsert_business_unit"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/graphql/resolvers"
	"inspection/services/inspection/internal/platform/httpboundary"
	"inspection/services/inspection/internal/platform/keycloak"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/operational"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/requestctx"
	process "inspection/services/inspection/internal/platform/runtime"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inspection-api:", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	metrics := observability.NewMetrics()
	// The operational service is constructed independently from the legacy OTP
	// notifiers. Producers migrate to this boundary in the next workflow stage.
	providerResolver, err := notifications.NewProviderResolver(notifications.Provider(cfg.Notification.WhatsAppProvider))
	if err != nil {
		return err
	}
	payloadCipher, err := notifications.NewPayloadCipher(cfg.Notification.ActivePayloadKey, cfg.Notification.PayloadKeys)
	if err != nil {
		return err
	}
	notificationService, err := notificationrequest.Setup(notificationrequest.Dependencies{DB: db, Providers: providerResolver, Payloads: payloadCipher, Metrics: metrics, V2ProducersEnabled: cfg.Notification.V2ProducersEnabled})
	if err != nil {
		return err
	}
	verifier, err := auth.NewRemoteVerifier(context.Background(), cfg.OIDCIssuer, cfg.OIDCAudiences, cfg.OIDCJWKSURL)
	if err != nil {
		return fmt.Errorf("initialize OIDC verifier: %w", err)
	}
	membershipStore := auth.GORMMembershipStore{DB: db}
	authenticator := auth.Authenticator{Verifier: verifier, Resolver: membershipStore, Audience: cfg.OIDCAudience, Audiences: cfg.OIDCAudiences}
	bus := mediator.New()
	authorizer := auth.Authorizer{Store: membershipStore, Scopes: auth.GORMScopeResolver{DB: db}}
	channelRegistry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{
		notifications.Email:    notifications.SMTPSender{Address: cfg.SMTPAddress, From: cfg.SMTPFrom},
		notifications.WhatsApp: notifications.TwilioSender{BaseURL: cfg.TwilioBaseURL, AccountSID: cfg.TwilioAccountSID, AuthToken: cfg.TwilioAuthToken, From: cfg.TwilioFrom, Channel: notifications.WhatsApp},
		notifications.SMS:      notifications.TwilioSender{BaseURL: cfg.TwilioBaseURL, AccountSID: cfg.TwilioAccountSID, AuthToken: cfg.TwilioAuthToken, From: cfg.TwilioFrom, Channel: notifications.SMS},
	})
	if err != nil {
		return err
	}
	limits := ratelimit.OTPPolicy{Limiter: ratelimit.Limiter{Store: ratelimit.DragonflyStore{Address: cfg.DragonflyAddress, Password: cfg.DragonflyPassword}}}
	invitationService := invitationcore.Service{DB: db, Pepper: []byte(cfg.OTPPepper), Limits: limits, Notifier: invitationcore.ChannelNotifier{Registry: channelRegistry, CallbackURL: cfg.TwilioCallbackURL}}
	onboardingService := onboardingsession.Service{DB: db, Pepper: []byte(cfg.OTPPepper), Limits: limits, Notifier: onboardingsession.RegistryNotifier{Registry: channelRegistry}}
	keycloakClient := keycloak.ProvisioningClient{BaseURL: cfg.KeycloakAdminURL, Realm: cfg.KeycloakRealm, ClientID: cfg.KeycloakClientID, ClientSecret: cfg.KeycloakClientSecret, Timeout: cfg.ProviderTimeout}
	activationService := adminactivation.Service{DB: db, Pepper: []byte(cfg.OTPPepper), Limits: limits, Notifier: onboardingsession.RegistryNotifier{Registry: channelRegistry}, Provider: keycloak.ActivationProvider{Client: keycloakClient}}
	bootstrapService := onboardingbootstrap.Service{DB: db}
	minioClient, err := objectstore.NewMinIO(cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOSecure)
	if err != nil {
		return err
	}
	publicMinioClient := minioClient
	if cfg.MinIOPublicEndpoint != cfg.MinIOEndpoint || cfg.MinIOPublicSecure != cfg.MinIOSecure {
		publicMinioClient, err = objectstore.NewMinIOWithRegion(cfg.MinIOPublicEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOPublicSecure, "us-east-1")
		if err != nil {
			return err
		}
	}
	mediaStore := objectstore.Store{Bucket: cfg.MinIOBucket, Client: minioClient, PublicClient: publicMinioClient}
	mediaService := mediacore.Service{DB: db, Store: mediaStore}
	originService := origincore.Service{DB: db}
	recaptureService := recapturecore.Service{DB: db}
	submissionFinalizer, err := capturefinalize.Setup(capturefinalize.Dependencies{Origins: originService, Recapture: recaptureService})
	if err != nil {
		return err
	}
	captureService := capturecore.Service{DB: db, Finalizer: submissionFinalizer}
	setups := []func() error{
		func() error {
			return acceptprocessing.Setup(acceptprocessing.Dependencies{Bus: bus, Service: invitationService})
		},
		func() error { return requestotp.Setup(requestotp.Dependencies{Bus: bus, Service: invitationService}) },
		func() error { return verifyotp.Setup(verifyotp.Dependencies{Bus: bus, Service: invitationService}) },
		func() error {
			return revokeinvitation.Setup(revokeinvitation.Dependencies{Bus: bus, Service: invitationService})
		},
		func() error {
			return createtenant.Setup(createtenant.Dependencies{DB: db, Bus: bus, SuperAdminIssuer: cfg.SuperAdminIssuer, SuperAdminSubject: cfg.SuperAdminSubject})
		},
		func() error {
			return listidentitymemberships.Setup(listidentitymemberships.Dependencies{DB: db, Bus: bus})
		},
		func() error { return createunit.Setup(createunit.Dependencies{DB: db, Bus: bus}) },
		func() error {
			return upsertunit.Setup(upsertunit.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return archiveunit.Setup(archiveunit.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return updatetenant.Setup(updatetenant.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return assignroles.Setup(assignroles.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return inviteinternal.Setup(inviteinternal.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return disablemembership.Setup(disablemembership.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return explaineffectiveaccess.Setup(explaineffectiveaccess.Dependencies{DB: db, Bus: bus, Authorizer: authorizer, Scopes: auth.GORMScopeResolver{DB: db}})
		},
		func() error { return recordaudit.Setup(recordaudit.Dependencies{DB: db, Bus: bus}) },
		func() error { return listaudit.Setup(listaudit.Dependencies{DB: db, Bus: bus, Authorizer: authorizer}) },
		func() error {
			return upsertparticipant.Setup(upsertparticipant.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return verifychannel.Setup(verifychannel.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return setchannels.Setup(setchannels.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return deactivateparticipant.Setup(deactivateparticipant.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return getparticipant.Setup(getparticipant.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return listparticipants.Setup(listparticipants.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return publishdefinition.Setup(publishdefinition.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return activatedefinition.Setup(activatedefinition.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return listdefinitions.Setup(listdefinitions.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error { return resolvedefinition.Setup(resolvedefinition.Dependencies{DB: db, Bus: bus}) },
		func() error {
			return publishprofile.Setup(publishprofile.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return publishtemplate.Setup(publishtemplate.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return activatetemplate.Setup(activatetemplate.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return listtemplates.Setup(listtemplates.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error { return resolvetemplate.Setup(resolvetemplate.Dependencies{DB: db, Bus: bus}) },
		func() error { return publishseeds.Setup(publishseeds.Dependencies{Bus: bus}) },
		func() error {
			return registerasset.Setup(registerasset.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return updateasset.Setup(updateasset.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return archiveasset.Setup(archiveasset.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error { return getasset.Setup(getasset.Dependencies{DB: db, Bus: bus, Authorizer: authorizer}) },
		func() error {
			return listassets.Setup(listassets.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error { return originresolve.Setup(originresolve.Dependencies{DB: db, Bus: bus}) },
		func() error {
			return inspectionoccurrence.Setup(inspectionoccurrence.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return inspectionmanual.Setup(inspectionmanual.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return inspectioncancel.Setup(inspectioncancel.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return inspectioninvalidate.Setup(inspectioninvalidate.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return inspectionget.Setup(inspectionget.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return inspectionlist.Setup(inspectionlist.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return schedulecreate.Setup(schedulecreate.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return scheduleupdate.Setup(scheduleupdate.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return schedulecancel.Setup(schedulecancel.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return schedulelist.Setup(schedulelist.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return projectcreate.Setup(projectcreate.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return projectadd.Setup(projectadd.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return projectstart.Setup(projectstart.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return projectskip.Setup(projectskip.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return projectclose.Setup(projectclose.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return projectreopen.Setup(projectreopen.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return projectget.Setup(projectget.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return projectlist.Setup(projectlist.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return origininvite.Setup(origininvite.Dependencies{DB: db, Bus: bus, Service: originService, Notifications: notificationService, CaptureBaseURL: cfg.CaptureOrigin, Authorizer: authorizer})
		},
		func() error {
			return originactivate.Setup(originactivate.Dependencies{DB: db, Bus: bus, Service: originService, Authorizer: authorizer})
		},
		func() error {
			return origininvalidate.Setup(origininvalidate.Dependencies{DB: db, Bus: bus, Service: originService, Authorizer: authorizer})
		},
		func() error {
			return originlist.Setup(originlist.Dependencies{DB: db, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return capturebootstrap.Setup(capturebootstrap.Dependencies{Bus: bus, Service: captureService})
		},
		func() error { return capturesave.Setup(capturesave.Dependencies{Bus: bus, Service: captureService}) },
		func() error {
			return capturedeclare.Setup(capturedeclare.Dependencies{Bus: bus, Service: captureService})
		},
		func() error {
			return capturesubmit.Setup(capturesubmit.Dependencies{Bus: bus, Service: captureService})
		},
		func() error { return mediacreate.Setup(mediacreate.Dependencies{Bus: bus, Service: mediaService}) },
		func() error { return mediapresign.Setup(mediapresign.Dependencies{Bus: bus, Service: mediaService}) },
		func() error {
			return mediareconcile.Setup(mediareconcile.Dependencies{Bus: bus, Service: mediaService})
		},
		func() error { return mediacomplete.Setup(mediacomplete.Dependencies{Bus: bus, Service: mediaService}) },
		func() error {
			return mediafalsepositive.Setup(mediafalsepositive.Dependencies{DB: db, Bus: bus, Service: mediaService})
		},
		func() error {
			return recapturerequest.Setup(recapturerequest.Dependencies{DB: db, Bus: bus, Service: recaptureService, Notifications: notificationService, CaptureBaseURL: cfg.CaptureOrigin, Authorizer: authorizer})
		},
		func() error {
			return recaptureexpire.Setup(recaptureexpire.Dependencies{Bus: bus, Service: recaptureService})
		},
		func() error {
			return recapturesubmit.Setup(recapturesubmit.Dependencies{DB: db, Bus: bus, Capture: captureService})
		},
	}
	for _, setup := range setups {
		if err := setup(); err != nil {
			return err
		}
	}
	mux := http.NewServeMux()
	if err := receivetwiliostatus.Setup(mux, receivetwiliostatus.Dependencies{DB: db, AuthToken: cfg.TwilioAuthToken, PublicURL: cfg.TwilioCallbackURL, AccountID: cfg.TwilioAccountSID, Metrics: metrics}); err != nil {
		return err
	}
	if cfg.Notification.WhatsAppProvider == "meta" {
		if err := receivemetastatus.Setup(mux, receivemetastatus.Dependencies{DB: db, VerifyToken: cfg.Notification.MetaVerifyToken, AppSecret: cfg.Notification.MetaAppSecret, AccountID: cfg.Notification.MetaPhoneNumberID, Metrics: metrics}); err != nil {
			return err
		}
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &resolvers.Resolver{Bus: bus, DB: db, Authorizer: authorizer, Store: mediaStore, Invitations: invitationService, Onboarding: onboardingService, AdminActivation: activationService, OnboardingBootstrap: bootstrapService, OwnerProvider: keycloakClient, OwnerIssuer: cfg.OIDCIssuer, ScheduleService: schedulecore.Service{DB: db, Bus: bus, Authorizer: authorizer}, InspectionService: inspectioncore.Service{DB: db, Bus: bus, Authorizer: authorizer}, ProjectService: projectcore.Service{DB: db, Bus: bus, Authorizer: authorizer}, PublicationService: publication.Service{DB: db}}}))
	server.SetErrorPresenter(graph.PresentError)
	mux.Handle("/graphql", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && cfg.Environment != "local" {
			http.NotFound(w, r)
			return
		}
		correlationID := r.Header.Get("X-Correlation-ID")
		if correlationID == "" {
			correlationID = identity.NewID().String()
		}
		w.Header().Set("X-Correlation-ID", correlationID)
		r.Header.Set("X-Correlation-ID", correlationID)
		ctx := requestctx.WithResponseWriter(r.Context(), w)
		if host, _, splitErr := net.SplitHostPort(r.RemoteAddr); splitErr == nil {
			ctx = requestctx.WithClientIP(ctx, host)
		} else if r.RemoteAddr != "" {
			ctx = requestctx.WithClientIP(ctx, r.RemoteAddr)
		}
		ctx = requestctx.WithSecureCookies(ctx, cfg.Environment != "local" || r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
		_, hasExternalCookie := r.Cookie("inspection_external")
		if r.Header.Get("Authorization") != "" && hasExternalCookie == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if cfg.TestAuthEnabled && r.Header.Get("X-Inspection-Test-Tenant") != "" {
			metadata, authErr := auth.TestMetadataFromHeaders(r)
			if authErr != nil {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			ctx = requestctx.WithMetadata(ctx, metadata)
		} else if r.Header.Get("Authorization") != "" {
			principal, authErr := authenticator.Authenticate(ctx, r.Header.Get("Authorization"))
			if authErr != nil {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			metadata := requestctx.Metadata{
				Principal:     principal,
				CorrelationID: correlationID, StartedAt: time.Now().UTC(),
			}
			if r.Header.Get(auth.MembershipHeader) == "" {
				if !isMembershipOptionalRequest(r) {
					http.Error(w, "access denied", http.StatusForbidden)
					return
				}
			} else {
				membershipID, parseErr := identity.ParseID(strings.TrimSpace(r.Header.Get(auth.MembershipHeader)))
				if parseErr != nil {
					http.Error(w, "access denied", http.StatusForbidden)
					return
				}
				resolved, resolveErr := membershipStore.ResolveMembership(ctx, principal.Issuer, principal.Subject, membershipID)
				if resolveErr != nil || resolved.Disabled {
					http.Error(w, "access denied", http.StatusForbidden)
					return
				}
				resolved.Audience, resolved.Product = principal.Audience, principal.Product
				metadata.TenantID, metadata.Principal = resolved.TenantID, resolved
			}
			ctx = requestctx.WithMetadata(ctx, metadata)
		}
		if cookie, cookieErr := r.Cookie("inspection_external"); cookieErr == nil {
			ctx = requestctx.WithExternalCredentials(ctx, requestctx.ExternalCredentials{SessionToken: cookie.Value, CSRFToken: r.Header.Get("X-CSRF-Token")})
		}
		if cookie, cookieErr := r.Cookie("inspection_onboarding"); cookieErr == nil {
			ctx = requestctx.WithOnboardingCredentials(ctx, requestctx.OnboardingCredentials{SessionToken: cookie.Value, CSRFToken: r.Header.Get("X-CSRF-Token")})
		}
		server.ServeHTTP(w, r.WithContext(requestctx.WithIdempotencyKey(ctx, r.Header.Get("Idempotency-Key"))))
	}))
	if err := operational.SetupWithMetrics(mux, func(r *http.Request) error { return database.Compatible(r.Context(), db, cfg.SchemaMin, cfg.SchemaMax) }, cfg.MetricsToken, metrics); err != nil {
		return err
	}
	return process.Serve(cfg.HTTPAddress, httpboundary.CORS(cfg.AllowedOrigins, cfg.CaptureOrigin, mux), cfg.ShutdownTimeout)
}

// isMembershipOptionalRequest allows only identity selection and tenant
// bootstrap before a membership has established tenant context. The GraphQL
// operation is restored after inspection so the handler sees the original body.
func isMembershipOptionalRequest(r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var payload struct {
		Query         string `json:"query"`
		OperationName string `json:"operationName"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || strings.TrimSpace(payload.Query) == "" {
		return false
	}
	document, err := parser.ParseQuery(&ast.Source{Name: "request.graphql", Input: payload.Query})
	if err != nil {
		return false
	}
	var operation *ast.OperationDefinition
	if payload.OperationName != "" {
		operation = document.Operations.ForName(payload.OperationName)
	} else if len(document.Operations) == 1 {
		operation = document.Operations[0]
	}
	if operation == nil {
		return false
	}
	allowed := map[string]struct{}{}
	switch operation.Operation {
	case ast.Query:
		allowed["me"] = struct{}{}
		allowed["tenant"] = struct{}{}
		allowed["onboardingSession"] = struct{}{}
	case ast.Mutation:
		allowed["createTenant"] = struct{}{}
		allowed["requestOnboardingOtp"] = struct{}{}
		allowed["verifyOnboardingOtp"] = struct{}{}
		allowed["saveOnboardingStep"] = struct{}{}
		allowed["requestAdminActivationOtp"] = struct{}{}
		allowed["verifyAdminActivationOtp"] = struct{}{}
		allowed["setAdminInitialPassword"] = struct{}{}
	default:
		return false
	}
	return rootSelectionsAllowed(operation.SelectionSet, document, allowed, map[string]bool{})
}

func rootSelectionsAllowed(selections ast.SelectionSet, document *ast.QueryDocument, allowed map[string]struct{}, visiting map[string]bool) bool {
	if len(selections) == 0 {
		return false
	}
	for _, selection := range selections {
		switch current := selection.(type) {
		case *ast.Field:
			if current.Name == "__typename" {
				continue
			}
			if _, ok := allowed[current.Name]; !ok {
				return false
			}
		case *ast.InlineFragment:
			if !rootSelectionsAllowed(current.SelectionSet, document, allowed, visiting) {
				return false
			}
		case *ast.FragmentSpread:
			if visiting[current.Name] {
				return false
			}
			fragment := document.Fragments.ForName(current.Name)
			if fragment == nil {
				return false
			}
			visiting[current.Name] = true
			accepted := rootSelectionsAllowed(fragment.SelectionSet, document, allowed, visiting)
			delete(visiting, current.Name)
			if !accepted {
				return false
			}
		default:
			return false
		}
	}
	return true
}
