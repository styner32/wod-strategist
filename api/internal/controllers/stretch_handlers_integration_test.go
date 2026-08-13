package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/controllers"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

func toJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}

var _ = Describe("Stretch Handlers Integration", func() {
	var (
		router *gin.Engine
		user   db.User
	)

	BeforeEach(func() {
		testhelpers.CleanupDB(dbConn)
		transport := testhelpers.NewMockTransport()
		storageClient, err := testhelpers.NewStorageClientWithSigning("test-bucket", transport)
		Expect(err).NotTo(HaveOccurred())
		router = newTestRouterWithAuthService(controllers.Config{
			StorageClient: storageClient,
			BucketName:    "test-bucket",
		})

		profile := testhelpers.CreateProfile(dbConn, &db.Profile{})
		Expect(dbConn.First(&user, profile.UserID).Error).NotTo(HaveOccurred())
	})

	Describe("GET /api/v1/stretches", func() {
		It("requires authentication", func() {
			req, err := http.NewRequest(http.MethodGet, "/api/v1/stretches", nil)
			Expect(err).NotTo(HaveOccurred())

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusUnauthorized))
		})

		It("returns empty array when catalog is empty", func() {
			req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/stretches", "", &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var resp []controllers.StretchResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp).To(BeEmpty())
		})

		It("returns stretches with aliases and signed media URLs", func() {
			s := testhelpers.CreateStretch(dbConn, &db.Stretch{
				Name:        "Couch Stretch",
				TargetArea:  "Hips & Glutes",
				ImageObject: "stretches/1/img.jpg",
				VideoObject: "stretches/1/video.mp4",
			})
			testhelpers.CreateStretchAlias(dbConn, &db.StretchAlias{
				StretchID: s.ID,
				Alias:     "Wall Quadriceps Stretch",
			})

			req := newAuthorizedJSONRequest(http.MethodGet, "/api/v1/stretches", "", &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var resp []controllers.StretchResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp).To(HaveLen(1))

			Expect(resp[0].Name).To(Equal("Couch Stretch"))
			Expect(resp[0].Aliases).To(ConsistOf("Wall Quadriceps Stretch"))
			Expect(resp[0].ImageURL).NotTo(BeEmpty())
			Expect(resp[0].VideoURL).NotTo(BeEmpty())
		})
	})

	Describe("POST /api/v1/stretches", func() {
		It("creates a new stretch with aliases", func() {
			body := controllers.StretchInputRequest{
				Name:         "Pigeon Pose",
				TargetArea:   "Hips & Glutes",
				Description:  "Relieves deep hip tightness",
				DurationHint: "90s per side",
				Caution:      "Protect knee",
				Aliases:      []string{"Glute Rotator Stretch", "Pigeon"},
			}

			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/stretches", toJSON(body), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusCreated))
			var resp controllers.StretchResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.ID).NotTo(BeZero())
			Expect(resp.Name).To(Equal("Pigeon Pose"))
			Expect(resp.Aliases).To(ConsistOf("Glute Rotator Stretch", "Pigeon"))

			var dbAliases []db.StretchAlias
			Expect(dbConn.Where("stretch_id = ?", resp.ID).Find(&dbAliases).Error).NotTo(HaveOccurred())
			Expect(dbAliases).To(HaveLen(2))
		})

		It("rejects missing or blank name with 400", func() {
			body := controllers.StretchInputRequest{
				Name:       "",
				TargetArea: "Hips",
			}
			req := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/stretches", toJSON(body), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
		})

		It("returns 409 when name collides with existing stretch or alias", func() {
			s := testhelpers.CreateStretch(dbConn, &db.Stretch{Name: "Couch Stretch"})
			testhelpers.CreateStretchAlias(dbConn, &db.StretchAlias{StretchID: s.ID, Alias: "Wall Quad Stretch"})

			// Attempt 1: Duplicate stretch name (case/hyphen insensitive)
			body1 := controllers.StretchInputRequest{Name: "couch-stretch"}
			req1 := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/stretches", toJSON(body1), &user)
			w1 := httptest.NewRecorder()
			router.ServeHTTP(w1, req1)
			Expect(w1.Code).To(Equal(http.StatusConflict))

			// Attempt 2: New stretch name collides with existing alias
			body2 := controllers.StretchInputRequest{Name: "wall quad stretch"}
			req2 := newAuthorizedJSONRequest(http.MethodPost, "/api/v1/stretches", toJSON(body2), &user)
			w2 := httptest.NewRecorder()
			router.ServeHTTP(w2, req2)
			Expect(w2.Code).To(Equal(http.StatusConflict))
		})
	})

	Describe("PUT /api/v1/stretches/:id", func() {
		It("updates stretch fields and replaces aliases", func() {
			s := testhelpers.CreateStretch(dbConn, &db.Stretch{Name: "Cat-Cow"})
			testhelpers.CreateStretchAlias(dbConn, &db.StretchAlias{StretchID: s.ID, Alias: "Cat Cow Pose"})

			body := controllers.StretchInputRequest{
				Name:         "Cat-Cow Flow",
				TargetArea:   "Spine & Back",
				Description:  "Updated spinal mobilization",
				DurationHint: "10 reps",
				Aliases:      []string{"Spine Segmental Flexion"},
			}

			url := fmt.Sprintf("/api/v1/stretches/%d", s.ID)
			req := newAuthorizedJSONRequest(http.MethodPut, url, toJSON(body), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var resp controllers.StretchResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Name).To(Equal("Cat-Cow Flow"))
			Expect(resp.Aliases).To(ConsistOf("Spine Segmental Flexion"))

			var count int64
			Expect(dbConn.Model(&db.StretchAlias{}).Where("stretch_id = ?", s.ID).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(1)))
		})

		It("returns 409 when stealing another stretch's key", func() {
			testhelpers.CreateStretch(dbConn, &db.Stretch{Name: "Couch Stretch"})
			s2 := testhelpers.CreateStretch(dbConn, &db.Stretch{Name: "Pigeon Pose"})

			body := controllers.StretchInputRequest{Name: "Couch Stretch"}
			url := fmt.Sprintf("/api/v1/stretches/%d", s2.ID)
			req := newAuthorizedJSONRequest(http.MethodPut, url, toJSON(body), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusConflict))
		})

		It("returns 404 for non-existent stretch", func() {
			body := controllers.StretchInputRequest{Name: "Non-existent"}
			req := newAuthorizedJSONRequest(http.MethodPut, "/api/v1/stretches/99999", toJSON(body), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusNotFound))
		})
	})

	Describe("DELETE /api/v1/stretches/:id", func() {
		It("deletes the stretch and cascades aliases", func() {
			s := testhelpers.CreateStretch(dbConn, &db.Stretch{Name: "Doorway Pec Stretch"})
			testhelpers.CreateStretchAlias(dbConn, &db.StretchAlias{StretchID: s.ID, Alias: "Pec Stretch"})

			url := fmt.Sprintf("/api/v1/stretches/%d", s.ID)
			req := newAuthorizedJSONRequest(http.MethodDelete, url, "", &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusNoContent))

			var stretchCount, aliasCount int64
			Expect(dbConn.Model(&db.Stretch{}).Where("id = ?", s.ID).Count(&stretchCount).Error).NotTo(HaveOccurred())
			Expect(stretchCount).To(Equal(int64(0)))
			Expect(dbConn.Model(&db.StretchAlias{}).Where("stretch_id = ?", s.ID).Count(&aliasCount).Error).NotTo(HaveOccurred())
			Expect(aliasCount).To(Equal(int64(0)))
		})

		It("returns 404 for unknown stretch ID", func() {
			req := newAuthorizedJSONRequest(http.MethodDelete, "/api/v1/stretches/99999", "", &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusNotFound))
		})
	})

	Describe("POST /api/v1/stretches/:id/media-upload-url", func() {
		It("returns signed upload URL and object_name with stretches/{id}/ prefix", func() {
			s := testhelpers.CreateStretch(dbConn, &db.Stretch{Name: "Hamstring Floss"})

			body := controllers.StretchMediaUploadURLRequest{
				MediaType: "video",
				Filename:  "demo.mp4",
			}
			url := fmt.Sprintf("/api/v1/stretches/%d/media-upload-url", s.ID)
			req := newAuthorizedJSONRequest(http.MethodPost, url, toJSON(body), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var resp controllers.StretchMediaUploadURLResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.UploadURL).NotTo(BeEmpty())
			Expect(resp.ObjectName).To(MatchRegexp(fmt.Sprintf("^stretches/%d/\\d+_video_demo\\.mp4$", s.ID)))
		})

		It("returns 400 for invalid media_type", func() {
			s := testhelpers.CreateStretch(dbConn, &db.Stretch{Name: "Wall Slide"})

			body := controllers.StretchMediaUploadURLRequest{
				MediaType: "audio",
				Filename:  "audio.mp3",
			}
			url := fmt.Sprintf("/api/v1/stretches/%d/media-upload-url", s.ID)
			req := newAuthorizedJSONRequest(http.MethodPost, url, toJSON(body), &user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("POST /api/v1/stretches/:id/media & DELETE /api/v1/stretches/:id/media", func() {
		It("sets stretch image and video media independently and clears media selectively", func() {
			s := testhelpers.CreateStretch(dbConn, &db.Stretch{Name: "Wrist Flexor Stretch"})
			imgObj := fmt.Sprintf("stretches/%d/12345_image_photo.jpg", s.ID)
			vidObj := fmt.Sprintf("stretches/%d/12345_video_demo.mp4", s.ID)

			setUrl := fmt.Sprintf("/api/v1/stretches/%d/media", s.ID)

			// 1. Set Image Media
			setImgBody := controllers.SetStretchMediaRequest{
				MediaType:  "image",
				ObjectName: imgObj,
			}
			reqImg := newAuthorizedJSONRequest(http.MethodPost, setUrl, toJSON(setImgBody), &user)
			wImg := httptest.NewRecorder()
			router.ServeHTTP(wImg, reqImg)

			Expect(wImg.Code).To(Equal(http.StatusOK))
			var respImg controllers.StretchResponse
			Expect(json.Unmarshal(wImg.Body.Bytes(), &respImg)).To(Succeed())
			Expect(respImg.ImageURL).NotTo(BeEmpty())
			Expect(respImg.VideoURL).To(BeEmpty())

			// 2. Set Video Media -> Now both Image and Video exist!
			setVidBody := controllers.SetStretchMediaRequest{
				MediaType:  "video",
				ObjectName: vidObj,
			}
			reqVid := newAuthorizedJSONRequest(http.MethodPost, setUrl, toJSON(setVidBody), &user)
			wVid := httptest.NewRecorder()
			router.ServeHTTP(wVid, reqVid)

			Expect(wVid.Code).To(Equal(http.StatusOK))
			var respBoth controllers.StretchResponse
			Expect(json.Unmarshal(wVid.Body.Bytes(), &respBoth)).To(Succeed())
			Expect(respBoth.ImageURL).NotTo(BeEmpty())
			Expect(respBoth.VideoURL).NotTo(BeEmpty())

			// 3. Clear Video Media specifically
			clearVidUrl := fmt.Sprintf("/api/v1/stretches/%d/media?kind=video", s.ID)
			reqClearVid := newAuthorizedJSONRequest(http.MethodDelete, clearVidUrl, "", &user)
			wClearVid := httptest.NewRecorder()
			router.ServeHTTP(wClearVid, reqClearVid)

			Expect(wClearVid.Code).To(Equal(http.StatusOK))
			var respAfterVidClear controllers.StretchResponse
			Expect(json.Unmarshal(wClearVid.Body.Bytes(), &respAfterVidClear)).To(Succeed())
			Expect(respAfterVidClear.ImageURL).NotTo(BeEmpty())
			Expect(respAfterVidClear.VideoURL).To(BeEmpty())

			// 4. Clear remaining media
			clearUrl := fmt.Sprintf("/api/v1/stretches/%d/media", s.ID)
			reqClear := newAuthorizedJSONRequest(http.MethodDelete, clearUrl, "", &user)
			wClear := httptest.NewRecorder()
			router.ServeHTTP(wClear, reqClear)

			Expect(wClear.Code).To(Equal(http.StatusOK))
			var respClear controllers.StretchResponse
			Expect(json.Unmarshal(wClear.Body.Bytes(), &respClear)).To(Succeed())
			Expect(respClear.ImageURL).To(BeEmpty())
			Expect(respClear.VideoURL).To(BeEmpty())
		})
	})
})
