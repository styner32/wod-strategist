package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("GET /api/v1/sessions/:session_id/on-device-ai", func() {
	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
	})

	It("requires profile ownership and pages only journal records across both formats", func() {
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
		other := testhelpers.CreateUser(dbConn, &db.User{})
		transport := testhelpers.NewMockTransport()
		client, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).To(Succeed())
		router := newTestRouterWithAuthService(controllers.Config{StorageClient: client, BucketName: "test-bucket"})
		sid := "WOD-20260921-01ARZ3NDEKTSV4RRFFQ69G5FAV"
		path := fmt.Sprintf("/api/v1/sessions/%s/on-device-ai?profile_id=%d", sid, profile.ID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &other))
		Expect(response.Code).To(Equal(http.StatusForbidden))
		prefix := fmt.Sprintf("videos/%d/%s/", profile.ID, sid)
		apple := []string{prefix + "apple_ai_old.json", prefix + "apple_ai_old_frame_1.jpg", "videos/999/other/apple_ai_stolen.json", prefix + "apple_ai_nested/secret.json", prefix + "video.mp4"}
		environment := []string{prefix + "environment_bundle_events.ndjson", prefix + "environment_bundle_session.json"}
		for i := 0; i < 52; i++ {
			environment = append(environment, fmt.Sprintf("%senvironment_%03d.json", prefix, i))
		}
		for i := 0; i < 2; i++ {
			testhelpers.MockGCSListObjects(transport, "test-bucket", prefix+"apple_ai_", apple)
			testhelpers.MockGCSListObjects(transport, "test-bucket", prefix+"environment_", environment)
		}
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path, "", &user))
		Expect(response.Code).To(Equal(http.StatusOK))
		var first struct {
			Files []string `json:"files"`
			Next  string   `json:"next_cursor"`
		}
		Expect(json.Unmarshal(response.Body.Bytes(), &first)).To(Succeed())
		Expect(first.Files).To(HaveLen(50))
		Expect(first.Files[0]).To(Equal("apple_ai_old.json"))
		Expect(first.Next).To(Equal(first.Files[49]))
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path+"&after="+first.Next, "", &user))
		var second struct {
			Files []string `json:"files"`
			Next  string   `json:"next_cursor"`
		}
		Expect(json.Unmarshal(response.Body.Bytes(), &second)).To(Succeed())
		Expect(second.Files).To(HaveLen(5))
		Expect(second.Files).To(ContainElements("environment_bundle_session.json", "environment_bundle_events.ndjson"))
		Expect(second.Next).To(BeEmpty())
		Expect(transport.Verify()).To(Succeed())
	})
	It("returns an empty list when no observation has been uploaded", func() {
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
		transport := testhelpers.NewMockTransport()
		client, err := testhelpers.NewStorageClient("test-bucket", transport)
		Expect(err).To(Succeed())
		router := newTestRouterWithAuthService(controllers.Config{StorageClient: client, BucketName: "test-bucket"})
		sid := "P7-WOD-2026-09-21-17-00"
		prefix := fmt.Sprintf("videos/%d/%s/", profile.ID, sid)
		for _, family := range []string{"apple_ai_", "environment_"} {
			testhelpers.MockGCSListObjects(transport, "test-bucket", prefix+family, nil)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%s/on-device-ai?profile_id=%d", sid, profile.ID), "", &user))
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(MatchJSON(`{"files":[],"next_cursor":""}`))
		Expect(transport.Verify()).To(Succeed())
	})
})

var _ = Describe("GET /api/v1/sessions/:session_id/on-device-ai/asset", func() {
	BeforeEach(func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
	})

	It("authorizes reads, preserves raw archives, bounds JSON and restricts signed media to the session", func() {
		profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		var user db.User
		Expect(dbConn.First(&user, profile.UserID).Error).To(Succeed())
		other := testhelpers.CreateUser(dbConn, &db.User{})
		transport := testhelpers.NewMockTransport()
		client, err := testhelpers.NewStorageClientWithSigning("test-bucket", transport)
		Expect(err).To(Succeed())
		router := newTestRouterWithAuthService(controllers.Config{StorageClient: client, BucketName: "test-bucket"})
		sid := "WOD-20260921-01ARZ3NDEKTSV4RRFFQ69G5FAV"
		path := fmt.Sprintf("/api/v1/sessions/%s/on-device-ai/asset?profile_id=%d&filename=", sid, profile.ID)
		prefix := fmt.Sprintf("videos/%d/%s/", profile.ID, sid)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path+"environment_one.json", nil))
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		response = httptest.NewRecorder()
		router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path+"environment_one.json", "", &other))
		Expect(response.Code).To(Equal(http.StatusForbidden))
		for _, name := range []string{"../environment_one.json", "environment_nested/one.json", "video.mp4", "sensor.json", "environment_one.html", "h10_memory_hr.m4a"} {
			response = httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path+url.QueryEscape(name), "", &user))
			Expect(response.Code).To(Equal(http.StatusBadRequest))
		}
		for _, item := range []struct {
			name, body string
			status     int
		}{
			{"apple_ai_one.json", `{"schemaVersion":1,"answer":{"feedback":"original answer"}}`, http.StatusOK},
			{"environment_one.json", `{"version":1,"outcome":"error","raw":"unparseable answer"}`, http.StatusOK},
			{"h10_memory_hr.json", `{"schema_version":1,"kind":"polar_h10_memory_hr","hr_samples":[90,91]}`, http.StatusOK},
			{"environment_broken.json", `{broken`, http.StatusUnprocessableEntity},
			{"environment_large.json", strings.Repeat("x", 2*1024*1024+1), http.StatusRequestEntityTooLarge},
		} {
			testhelpers.MockGCSDownloadWithBody(transport, "gs://test-bucket/"+prefix+item.name, []byte(item.body))
			response = httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path+item.name, "", &user))
			Expect(response.Code).To(Equal(item.status), response.Body.String())
			Expect(response.Header().Get("Cache-Control")).To(Equal("private, no-store"))
			if item.status == http.StatusOK {
				Expect(response.Body.String()).To(Equal(item.body))
			}
		}
		for _, name := range []string{"apple_ai_one_input_1.jpg", "environment_one_0.m4a", "environment_one_events.ndjson"} {
			response = httptest.NewRecorder()
			router.ServeHTTP(response, newAuthorizedJSONRequest(http.MethodGet, path+name, "", &user))
			Expect(response.Code).To(Equal(http.StatusTemporaryRedirect))
			link, err := url.Parse(response.Header().Get("Location"))
			Expect(err).To(Succeed())
			Expect(link.Path).To(Equal("/test-bucket/" + prefix + name))
			Expect(link.Query().Get("X-Goog-Expires")).To(BeElementOf("899", "900"))
		}
		Expect(transport.Verify()).To(Succeed())
	})
})
