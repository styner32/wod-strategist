package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/cost"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/gemini"
	"github.com/wod-strategist/api/internal/testhelpers"
	"go.uber.org/zap"
)

func imageUsageSpec(route, task string) {
	It("records measured usage for an empty response against the authenticated account", func() {
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		user := testhelpers.CreateUser(dbConn, &db.User{})
		transport := testhelpers.NewMockTransport()
		client, err := gemini.NewClientWithOptions(context.Background(), zap.NewNop(), gemini.Options{APIKey: "test", HTTPClient: &http.Client{Transport: transport}})
		Expect(err).NotTo(HaveOccurred())
		transport.New("https://generativelanguage.googleapis.com").Post("/v1beta/models/" + gemini.ModelFlash38 + ":generateContent").Reply(200).Body([]byte(`{"usageMetadata":{"promptTokenCount":15,"thoughtsTokenCount":8,"totalTokenCount":23}}`))
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, err := form.CreateFormFile("image", "photo.png")
		Expect(err).NotTo(HaveOccurred())
		Expect(png.Encode(part, image.NewRGBA(image.Rect(0, 0, 4, 4)))).To(Succeed())
		Expect(form.Close()).To(Succeed())
		req, err := http.NewRequest(http.MethodPost, route, &body)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+generateValidToken(user))
		response := httptest.NewRecorder()
		newTestRouterWithAuthService(controllers.Config{ImageParser: client}).ServeHTTP(response, req)
		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		var records []db.TokenUsage
		Expect(dbConn.Find(&records).Error).To(Succeed())
		Expect(records).To(HaveLen(1))
		Expect(records[0].TaskType).To(Equal(task))
		Expect(records[0].UserID).To(HaveValue(Equal(user.ID)))
		Expect(records[0].ProfileID).To(BeZero())
		Expect(records[0].SessionID).To(BeEmpty())
		Expect(records[0].TotalTokens).To(Equal(int64(23)))
		// Users without profiles still see their own pre-session usage.
		totalResponse := httptest.NewRecorder()
		newTestRouterWithAuthService(controllers.Config{}).ServeHTTP(totalResponse, newAuthorizedJSONRequest(http.MethodGet, "/api/v1/analytics/cost", "", &user))
		Expect(totalResponse.Code).To(Equal(http.StatusOK))
		var total cost.TotalCostResponse
		Expect(json.Unmarshal(totalResponse.Body.Bytes(), &total)).To(Succeed())
		Expect(total.TotalTokens).To(Equal(int64(23)))
		Expect(total.UnmeasuredCalls).To(Equal(int64(1)))

		Expect(transport.Verify()).To(Succeed())
	})
}

var _ = Describe("POST /api/v1/parse-workout-image", func() { imageUsageSpec("/api/v1/parse-workout-image", "image:workout") })
var _ = Describe("POST /api/v1/appearance-from-image", func() { imageUsageSpec("/api/v1/appearance-from-image", "image:appearance") })
