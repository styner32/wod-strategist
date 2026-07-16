package auth_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/wod-strategist/api/internal/auth"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("Auth Service", func() {
	var (
		dbConn *gorm.DB
		svc    *auth.Service
		ctx    context.Context
	)

	testSecret := []byte("test-jwt-secret-32-bytes-long!!!")

	BeforeEach(func() {
		var err error
		dbConn, err = testhelpers.InitDB()
		Expect(err).NotTo(HaveOccurred())
		testhelpers.CleanupDB(dbConn)

		svc = auth.NewService(dbConn, testSecret)
		ctx = context.Background()
	})

	// -------------------------------------------------------------------
	// Signup
	// -------------------------------------------------------------------
	Describe("Signup", func() {
		It("creates a user, default profile, and returns a valid JWT", func() {
			token, userID, err := svc.Signup(ctx, "testuser", "password123")
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())
			Expect(userID).NotTo(BeZero())

			// Verify user in DB
			var user db.User
			Expect(dbConn.Where("id = ?", userID).First(&user).Error).NotTo(HaveOccurred())
			Expect(user.Username).To(Equal("testuser"))
			Expect(user.PasswordHash).NotTo(Equal("password123")) // should be hashed
			Expect(user.TokenVersion).To(Equal(1))
			Expect(user.DeletedAt).To(BeNil())

			// Verify default profile was created
			var profile db.Profile
			Expect(dbConn.Where("user_id = ?", userID).First(&profile).Error).NotTo(HaveOccurred())
			Expect(profile.Name).To(Equal("testuser"))
			Expect(profile.FitnessLevel).To(Equal("intermediate"))

			// Verify token is valid
			validatedUserID, err := svc.ValidateToken(ctx, token)
			Expect(err).NotTo(HaveOccurred())
			Expect(validatedUserID).To(Equal(userID))
		})

		It("rejects invalid usernames", func() {
			_, _, err := svc.Signup(ctx, "AB", "password123")
			Expect(err).To(MatchError(auth.ErrInvalidUsername))

			_, _, err = svc.Signup(ctx, "UPPERCASE", "password123")
			Expect(err).To(MatchError(auth.ErrInvalidUsername))

			_, _, err = svc.Signup(ctx, "has space", "password123")
			Expect(err).To(MatchError(auth.ErrInvalidUsername))
		})

		It("rejects short passwords", func() {
			_, _, err := svc.Signup(ctx, "testuser", "short")
			Expect(err).To(MatchError(auth.ErrPasswordTooShort))
		})

		It("rejects duplicate usernames", func() {
			_, _, err := svc.Signup(ctx, "testuser", "password123")
			Expect(err).NotTo(HaveOccurred())

			_, _, err = svc.Signup(ctx, "testuser", "password456")
			Expect(err).To(MatchError(auth.ErrUsernameTaken))
		})

		It("rejects duplicate usernames case-insensitively", func() {
			_, _, err := svc.Signup(ctx, "testuser", "password123")
			Expect(err).NotTo(HaveOccurred())

			// The username validation regex enforces lowercase, so this should
			// fail at the regex level before hitting the DB constraint.
			_, _, err = svc.Signup(ctx, "TESTUSER", "password456")
			Expect(err).To(MatchError(auth.ErrInvalidUsername))
		})
	})

	// -------------------------------------------------------------------
	// Login
	// -------------------------------------------------------------------
	Describe("Login", func() {
		BeforeEach(func() {
			_, _, err := svc.Signup(ctx, "loginuser", "password123")
			Expect(err).NotTo(HaveOccurred())
		})

		It("authenticates with correct credentials", func() {
			token, userID, err := svc.Login(ctx, "loginuser", "password123")
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())
			Expect(userID).NotTo(BeZero())
		})

		It("rejects wrong password", func() {
			_, _, err := svc.Login(ctx, "loginuser", "wrongpassword")
			Expect(err).To(MatchError(auth.ErrInvalidCredentials))
		})

		It("rejects non-existent user", func() {
			_, _, err := svc.Login(ctx, "noone", "password123")
			Expect(err).To(MatchError(auth.ErrInvalidCredentials))
		})
	})

	// -------------------------------------------------------------------
	// ValidateToken
	// -------------------------------------------------------------------
	Describe("ValidateToken", func() {
		It("validates a freshly issued token", func() {
			token, userID, err := svc.Signup(ctx, "validateuser", "password123")
			Expect(err).NotTo(HaveOccurred())

			validatedID, err := svc.ValidateToken(ctx, token)
			Expect(err).NotTo(HaveOccurred())
			Expect(validatedID).To(Equal(userID))
		})

		It("rejects a garbage token", func() {
			_, err := svc.ValidateToken(ctx, "not-a-jwt")
			Expect(err).To(HaveOccurred())
		})

		It("rejects a token with wrong secret", func() {
			wrongSvc := auth.NewService(dbConn, []byte("wrong-secret-key-32-bytes-long!!"))
			token, _, err := wrongSvc.Signup(ctx, "wrongsecret", "password123")
			Expect(err).NotTo(HaveOccurred())

			// Validate with the original service (different secret)
			_, err = svc.ValidateToken(ctx, token)
			Expect(err).To(HaveOccurred())
		})
	})

	// -------------------------------------------------------------------
	// Logout
	// -------------------------------------------------------------------
	Describe("Logout", func() {
		It("invalidates existing tokens by bumping token_version", func() {
			token, userID, err := svc.Signup(ctx, "logoutuser", "password123")
			Expect(err).NotTo(HaveOccurred())

			// Token should be valid before logout
			_, err = svc.ValidateToken(ctx, token)
			Expect(err).NotTo(HaveOccurred())

			// Logout
			Expect(svc.Logout(ctx, userID)).To(Succeed())

			// Fresh service (no cache) — old token should fail
			freshSvc := auth.NewService(dbConn, testSecret)
			_, err = freshSvc.ValidateToken(ctx, token)
			Expect(err).To(HaveOccurred())

			// New login should still work
			newToken, _, err := svc.Login(ctx, "logoutuser", "password123")
			Expect(err).NotTo(HaveOccurred())

			_, err = freshSvc.ValidateToken(ctx, newToken)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	// -------------------------------------------------------------------
	// DeleteAccount
	// -------------------------------------------------------------------
	Describe("DeleteAccount", func() {
		var (
			userID    uint
			profileID uint
		)

		BeforeEach(func() {
			var err error
			_, userID, err = svc.Signup(ctx, "deleteuser", "password123")
			Expect(err).NotTo(HaveOccurred())

			// Seed some analysis data so we can verify cascade
			var profile db.Profile
			Expect(dbConn.Where("user_id = ?", userID).First(&profile).Error).NotTo(HaveOccurred())
			profileID = profile.ID

			testhelpers.CreateAnalysisResult(dbConn, &db.AnalysisResult{
				SessionID: "sess-delete-001",
				ProfileID: profile.ID,
				Status:    "COMPLETED",
				Output:    "test",
			})

			chunk := testhelpers.CreateChunkAnalysisResult(dbConn, &db.ChunkAnalysisResult{
				SessionID: "sess-delete-001",
				ProfileID: profile.ID,
				Status:    "COMPLETED",
				Output:    "chunk",
			})

			testhelpers.CreateAnalysisFeedback(dbConn, &db.AnalysisFeedback{
				ProfileID:             profile.ID,
				SessionID:             "sess-delete-001",
				TargetType:            db.FeedbackTargetChunk,
				ChunkAnalysisResultID: &chunk.ID,
				Category:              db.FeedbackCategoryMovement,
				OriginalPrediction:    db.JSONDocument(`{"exercise_type":"Rope Climb"}`),
				Correction:            db.JSONDocument(`{"movement_name":"Pull-up"}`),
			})
			testhelpers.CreateChunkReanalysisRun(dbConn, &db.ChunkReanalysisRun{
				SessionID:             "sess-delete-001",
				ProfileID:             profile.ID,
				ChunkAnalysisResultID: chunk.ID,
				Status:                db.ChunkReanalysisStatusCompleted,
			})
			testhelpers.CreateSessionReanalysisRun(dbConn, &db.SessionReanalysisRun{
				SessionID: "sess-delete-001",
				ProfileID: profile.ID,
				Status:    db.SessionReanalysisStatusCompleted,
			})

			Expect(dbConn.Create(&db.TokenUsage{
				SessionID:       "sess-delete-001",
				ProfileID:       profile.ID,
				TaskType:        "video:analysis",
				Model:           "gemini-test",
				PromptTokens:    100,
				CandidateTokens: 50,
				TotalTokens:     150,
			}).Error).NotTo(HaveOccurred())
		})

		It("soft-deletes user and cascades leaf data", func() {
			gcsPrefixes, err := svc.DeleteAccount(ctx, userID, "password123")
			Expect(err).NotTo(HaveOccurred())
			Expect(gcsPrefixes).NotTo(BeEmpty())

			// User should be soft-deleted
			var user db.User
			Expect(dbConn.Where("id = ?", userID).First(&user).Error).NotTo(HaveOccurred())
			Expect(user.DeletedAt).NotTo(BeNil())
			Expect(user.PasswordHash).To(Equal("DELETED"))

			// Profiles should be hard-deleted
			var profileCount int64
			dbConn.Model(&db.Profile{}).Where("user_id = ?", userID).Count(&profileCount)
			Expect(profileCount).To(BeZero())

			// Leaf data should be gone
			var analysisCount int64
			dbConn.Model(&db.AnalysisResult{}).Where("session_id = ?", "sess-delete-001").Count(&analysisCount)
			Expect(analysisCount).To(BeZero())

			var chunkCount int64
			dbConn.Model(&db.ChunkAnalysisResult{}).Where("session_id = ?", "sess-delete-001").Count(&chunkCount)
			Expect(chunkCount).To(BeZero())

			var tokenCount int64
			dbConn.Model(&db.TokenUsage{}).Where("session_id = ?", "sess-delete-001").Count(&tokenCount)
			Expect(tokenCount).To(BeZero())

			var feedbackCount int64
			dbConn.Model(&db.AnalysisFeedback{}).Where("profile_id = ?", profileID).Count(&feedbackCount)
			Expect(feedbackCount).To(BeZero())

			var reanalysisCount int64
			dbConn.Model(&db.ChunkReanalysisRun{}).Where("profile_id = ?", profileID).Count(&reanalysisCount)
			Expect(reanalysisCount).To(BeZero())

			var sessionReanalysisCount int64
			dbConn.Model(&db.SessionReanalysisRun{}).Where("profile_id = ?", profileID).Count(&sessionReanalysisCount)
			Expect(sessionReanalysisCount).To(BeZero())
		})

		It("rejects wrong password", func() {
			_, err := svc.DeleteAccount(ctx, userID, "wrongpassword")
			Expect(err).To(MatchError(auth.ErrInvalidCredentials))
		})

		It("rejects login after deletion", func() {
			_, err := svc.DeleteAccount(ctx, userID, "password123")
			Expect(err).NotTo(HaveOccurred())

			_, _, err = svc.Login(ctx, "deleteuser", "password123")
			Expect(err).To(MatchError(auth.ErrInvalidCredentials))
		})
	})

	// -------------------------------------------------------------------
	// Password helpers
	// -------------------------------------------------------------------
	Describe("Password helpers", func() {
		It("hashes and verifies correctly", func() {
			hash, err := auth.HashPassword("mypassword")
			Expect(err).NotTo(HaveOccurred())
			Expect(hash).NotTo(Equal("mypassword"))

			Expect(auth.VerifyPassword("mypassword", hash)).To(BeTrue())
			Expect(auth.VerifyPassword("wrongpassword", hash)).To(BeFalse())
		})
	})

	// -------------------------------------------------------------------
	// JWT helpers
	// -------------------------------------------------------------------
	Describe("JWT helpers", func() {
		It("issues and parses a valid token", func() {
			token, err := auth.IssueToken(testSecret, 123, "testuser", 1)
			Expect(err).NotTo(HaveOccurred())

			claims, err := auth.ParseToken(testSecret, token)
			Expect(err).NotTo(HaveOccurred())
			Expect(claims.UserID).To(Equal(uint(123)))
			Expect(claims.TokenVersion).To(Equal(1))
			Expect(claims.Subject).To(Equal("testuser"))
		})

		It("rejects a token with a wrong secret", func() {
			token, err := auth.IssueToken(testSecret, 123, "testuser", 1)
			Expect(err).NotTo(HaveOccurred())

			_, err = auth.ParseToken([]byte("different-secret"), token)
			Expect(err).To(HaveOccurred())
		})
	})

})
