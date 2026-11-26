package session

import (
	"context"
	cryptoRand "crypto/rand"
	"errors"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
	"github.com/wod-strategist/api/internal/testhelpers"
	"github.com/wod-strategist/api/internal/wodeval"
)

func expectCanonicalSessionID(sessionID, expectedPrefix, beforeUTCDate, afterUTCDate string) {
	GinkgoHelper()

	matches := canonicalSessionIDPattern.FindStringSubmatch(sessionID)
	Expect(matches).To(HaveLen(4), "expected canonical session ID, got %q", sessionID)
	Expect(matches[1]).To(Equal(expectedPrefix))
	Expect(matches[2]).To(BeElementOf(beforeUTCDate, afterUTCDate))

	parsed, err := ulid.ParseStrict(matches[3])
	Expect(err).NotTo(HaveOccurred(), "expected valid ULID in %q", sessionID)
	Expect(parsed.Time()).To(BeNumerically(">", 0))
}

var _ = Describe("Create", func() {
	var (
		ctx          context.Context
		profile      db.Profile
		otherProfile db.Profile
	)

	BeforeEach(func() {
		ctx = context.Background()
		ensureTestDB()
		testhelpers.CleanupDB(dbConn)
		profile = testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Athlete"})
		otherProfile = testhelpers.CreateProfile(dbConn, &db.Profile{Name: "Other Athlete"})
	})

	Context("input validation", func() {
		It("rejects a zero profile_id with ErrInvalidProfileID", func() {
			_, err := Create(ctx, dbConn, CreateSessionAttr{ProfileID: 0})
			Expect(errors.Is(err, ErrInvalidProfileID)).To(BeTrue(), "got err=%v", err)

			var count int64
			Expect(dbConn.Model(&db.Session{}).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(0)))
		})

		DescribeTable("rejects invalid client-supplied session_id values with ErrInvalidSessionID",
			func(rawSessionID string) {
				_, err := Create(ctx, dbConn, CreateSessionAttr{
					ProfileID:      profile.ID,
					SessionID:      rawSessionID,
					IdempotencyKey: "invalid-sid-key",
				})
				Expect(errors.Is(err, ErrInvalidSessionID)).To(BeTrue(), "session_id=%q got err=%v", rawSessionID, err)

				var count int64
				Expect(dbConn.Model(&db.Session{}).Count(&count).Error).NotTo(HaveOccurred())
				Expect(count).To(Equal(int64(0)))
			},
			Entry("lowercase prefix", "wod-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"),
			Entry("lowercase ULID", "WOD-20260407-01arz3ndektsv4rrffq69g5fav"),
			Entry("legacy P{id}- format", "P1-WOD-2026-04-07-10-00"),
			Entry("previous 12-digit server format", "WOD-202604071030-01ARZ3NDEKTSV4RRFFQ69G5FAV"),
			Entry("unsupported type prefix", "REHAB-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"),
			Entry("short ULID", "WOD-20260407-01ARZ3NDEK"),
			Entry("invalid Crockford base32 char", "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAI"),
			Entry("path traversal", "../WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"),
		)

		It("rejects mismatched explicit workout_type and client session_id prefix with ErrWorkoutTypeMismatch", func() {
			for _, workoutType := range []string{"wod", "rehab"} {
				_, err := Create(ctx, dbConn, CreateSessionAttr{
					ProfileID:      profile.ID,
					SessionID:      "WARMUP-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV",
					WorkoutType:    workoutType,
					IdempotencyKey: "mismatch-key-" + workoutType,
				})
				Expect(errors.Is(err, ErrWorkoutTypeMismatch)).To(BeTrue(), "workout_type=%q got err=%v", workoutType, err)
			}

			var count int64
			Expect(dbConn.Model(&db.Session{}).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(0)))
		})
	})

	Context("server-generated session IDs", func() {
		DescribeTable("generates canonical {TYPE}-YYYYMMDD-{ULID} and sets wod_evaluation_version per workout_type",
			func(rawWorkoutType, wantType, wantPrefix string, wantEvalVersion int) {
				beforeDate := time.Now().UTC().Format("20060102")
				created, err := Create(ctx, dbConn, CreateSessionAttr{
					ProfileID:   profile.ID,
					WorkoutType: rawWorkoutType,
				})
				afterDate := time.Now().UTC().Format("20060102")

				Expect(err).NotTo(HaveOccurred())
				expectCanonicalSessionID(created.SessionID, wantPrefix, beforeDate, afterDate)
				Expect(created.Status).To(Equal(db.SessionStatus(db.SessionStatusStarted)))
				Expect(created.WorkoutType).To(Equal(wantType))
				Expect(created.WODEvaluationVersion).To(Equal(wantEvalVersion))

				var stored db.Session
				Expect(dbConn.Where("id = ?", created.ID).First(&stored).Error).NotTo(HaveOccurred())
				Expect(stored.SessionID).To(Equal(created.SessionID))
				Expect(stored.Status).To(Equal(db.SessionStatus(db.SessionStatusStarted)))
				Expect(stored.WorkoutType).To(Equal(wantType))
				Expect(stored.WODEvaluationVersion).To(Equal(wantEvalVersion))
				Expect(string(stored.MovementHints)).To(MatchJSON(`[]`))
			},
			Entry("defaults empty workout_type to wod", "", "wod", "WOD", wodeval.Version),
			Entry("explicit wod", "wod", "wod", "WOD", wodeval.Version),
			Entry("warmup", "warmup", "warmup", "WARMUP", 0),
			Entry("uppercase accessory", "ACCESSORY", "accessory", "ACCESSORY", 0),
			Entry("trimmed cooldown", "  cooldown  ", "cooldown", "COOLDOWN", 0),
			Entry("normalizes unknown workout_type to wod", "rehab", "wod", "WOD", wodeval.Version),
		)

		It("normalizes movement hints and derives fallback WODDescription when description is omitted", func() {
			created, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				WorkoutType:    "wod",
				WODDescription: "   ",
				Movements:      []string{"  Pull-up ", "pull-up", "Sandbag Over Shoulder", "   ", ""},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(created.MovementHints)).To(MatchJSON(`["Pull-up", "Sandbag Over Shoulder"]`))
			Expect(created.WODDescription).To(Equal("Individual movements: Pull-up,Sandbag Over Shoulder"))
		})

		It("preserves explicit WODDescription over movement hints fallback", func() {
			created, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				WorkoutType:    "wod",
				WODDescription: "  Fran 21-15-9  ",
				Movements:      []string{"Thruster", "Pull-up"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(created.WODDescription).To(Equal("Fran 21-15-9"))
			Expect(string(created.MovementHints)).To(MatchJSON(`["Thruster", "Pull-up"]`))
		})

		It("creates distinct sessions when idempotency_key is empty or whitespace", func() {
			s1, err := Create(ctx, dbConn, CreateSessionAttr{ProfileID: profile.ID, IdempotencyKey: ""})
			Expect(err).NotTo(HaveOccurred())
			s2, err := Create(ctx, dbConn, CreateSessionAttr{ProfileID: profile.ID, IdempotencyKey: ""})
			Expect(err).NotTo(HaveOccurred())
			s3, err := Create(ctx, dbConn, CreateSessionAttr{ProfileID: profile.ID, IdempotencyKey: "   "})
			Expect(err).NotTo(HaveOccurred())

			Expect([]string{s1.SessionID, s2.SessionID, s3.SessionID}).To(HaveLen(3))
			Expect(s1.SessionID).NotTo(Equal(s2.SessionID))
			Expect(s2.SessionID).NotTo(Equal(s3.SessionID))
			Expect(s1.SessionID).NotTo(Equal(s3.SessionID))
			Expect(s1.IdempotencyKey).To(Equal(""))
			Expect(s2.IdempotencyKey).To(Equal(""))
			Expect(s3.IdempotencyKey).To(Equal(""))

			var count int64
			Expect(dbConn.Model(&db.Session{}).Where("profile_id = ?", profile.ID).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(3)))
		})
	})

	Context("client-supplied session IDs", func() {
		It("stores the client-supplied session_id and infers workout_type from its prefix when omitted", func() {
			const sid = "WARMUP-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"

			created, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid,
				IdempotencyKey: "idem-client-1",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(created.SessionID).To(Equal(sid))
			Expect(created.WorkoutType).To(Equal("warmup"))
			Expect(created.WODEvaluationVersion).To(Equal(0))
			Expect(created.Status).To(Equal(db.SessionStatus(db.SessionStatusStarted)))
		})

		It("accepts a client-supplied session_id whose local YYYYMMDD differs from the ULID UTC date", func() {
			utcTime := time.Date(2026, 4, 7, 15, 30, 0, 0, time.UTC)
			sid := "WOD-20260408-" + ulid.MustNew(ulid.Timestamp(utcTime), cryptoRand.Reader).String()

			created, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid,
				IdempotencyKey: "idem-tz-diff",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(created.SessionID).To(Equal(sid))
			Expect(created.WorkoutType).To(Equal("wod"))
			Expect(created.WODEvaluationVersion).To(Equal(wodeval.Version))
		})

		It("accepts a client-supplied session_id when explicit workout_type matches the prefix", func() {
			const sid = "ACCESSORY-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"

			created, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid,
				WorkoutType:    "  Accessory ",
				IdempotencyKey: "idem-accessory-match",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(created.SessionID).To(Equal(sid))
			Expect(created.WorkoutType).To(Equal("accessory"))
			Expect(created.WODEvaluationVersion).To(Equal(0))
		})
	})

	Context("idempotency and conflict resolution", func() {
		It("returns the existing session without minting a new ID when server-generated request retries with the same idempotency_key", func() {
			first, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				IdempotencyKey: "  idem-server-1  ",
				WorkoutType:    "wod",
				WODDescription: "Fran",
			})
			Expect(err).NotTo(HaveOccurred())

			second, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				IdempotencyKey: "idem-server-1",
				WorkoutType:    "wod",
				WODDescription: "Grace",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(second.ID).To(Equal(first.ID))
			Expect(second.SessionID).To(Equal(first.SessionID))
			Expect(second.IdempotencyKey).To(Equal("idem-server-1"))
			Expect(second.WODDescription).To(Equal("Fran"))

			var count int64
			Expect(dbConn.Model(&db.Session{}).Where("profile_id = ?", profile.ID).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(1)))
		})

		It("allows the same idempotency_key on a different profile", func() {
			s1, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				IdempotencyKey: "shared-key",
			})
			Expect(err).NotTo(HaveOccurred())

			s2, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      otherProfile.ID,
				IdempotencyKey: "shared-key",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(s1.SessionID).NotTo(Equal(s2.SessionID))

			var count int64
			Expect(dbConn.Model(&db.Session{}).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(2)))
		})

		It("returns the existing session when client-supplied session_id retries with the same profile and idempotency_key", func() {
			const sid = "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"

			first, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid,
				IdempotencyKey: "idem-c1",
				WODDescription: "Murph",
			})
			Expect(err).NotTo(HaveOccurred())

			retrySameKey, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid,
				IdempotencyKey: "idem-c1",
				WODDescription: "Changed",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(retrySameKey.ID).To(Equal(first.ID))
			Expect(retrySameKey.SessionID).To(Equal(sid))
			Expect(retrySameKey.WODDescription).To(Equal("Murph"))

			var count int64
			Expect(dbConn.Model(&db.Session{}).Where("profile_id = ?", profile.ID).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(1)))
		})

		It("returns the existing session when an existing row has an empty idempotency_key and client retries with a key", func() {
			const sid = "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"
			seeded := db.Session{
				SessionID:      sid,
				ProfileID:      profile.ID,
				Status:         db.SessionStatusStarted,
				IdempotencyKey: "",
				WODDescription: "Existing",
				WorkoutType:    "wod",
				MovementHints:  db.JSONDocument(`[]`),
			}
			Expect(dbConn.Create(&seeded).Error).NotTo(HaveOccurred())

			got, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid,
				IdempotencyKey: "idem-late",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.ID).To(Equal(seeded.ID))
			Expect(got.SessionID).To(Equal(sid))

			var count int64
			Expect(dbConn.Model(&db.Session{}).Where("profile_id = ?", profile.ID).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(1)))
		})

		It("returns ErrSessionProfileConflict when client session_id belongs to another profile", func() {
			const sid = "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"

			_, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid,
				IdempotencyKey: "idem-owner",
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      otherProfile.ID,
				SessionID:      sid,
				IdempotencyKey: "idem-other",
			})
			Expect(errors.Is(err, ErrSessionProfileConflict)).To(BeTrue(), "got err=%v", err)

			var stored db.Session
			Expect(dbConn.Where("session_id = ?", sid).First(&stored).Error).NotTo(HaveOccurred())
			Expect(stored.ProfileID).To(Equal(profile.ID))
		})

		It("returns ErrIdempotencyConflict when client session_id exists on the same profile with a different idempotency_key", func() {
			const sid = "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"

			_, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid,
				IdempotencyKey: "idem-1",
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid,
				IdempotencyKey: "idem-2",
			})
			Expect(errors.Is(err, ErrIdempotencyConflict)).To(BeTrue(), "got err=%v", err)
		})

		It("returns ErrIdempotencyConflict when idempotency_key is already bound to a different session_id on the same profile", func() {
			const (
				sid1 = "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"
				sid2 = "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAW"
			)

			_, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid1,
				IdempotencyKey: "idem-bound",
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid2,
				IdempotencyKey: "idem-bound",
			})
			Expect(errors.Is(err, ErrIdempotencyConflict)).To(BeTrue(), "got err=%v", err)

			var count int64
			Expect(dbConn.Model(&db.Session{}).Where("profile_id = ?", profile.ID).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(1)))
		})

		It("returns ErrIdempotencyConflict when session_id exists with empty key but idempotency_key is already bound to another session", func() {
			const (
				sid1 = "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"
				sid2 = "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAW"
			)
			Expect(dbConn.Create(&db.Session{
				SessionID:      sid1,
				ProfileID:      profile.ID,
				Status:         db.SessionStatusStarted,
				IdempotencyKey: "",
				WorkoutType:    "wod",
				MovementHints:  db.JSONDocument(`[]`),
			}).Error).NotTo(HaveOccurred())

			_, err := Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid2,
				IdempotencyKey: "idem-taken",
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = Create(ctx, dbConn, CreateSessionAttr{
				ProfileID:      profile.ID,
				SessionID:      sid1,
				IdempotencyKey: "idem-taken",
			})
			Expect(errors.Is(err, ErrIdempotencyConflict)).To(BeTrue(), "got err=%v", err)
		})
	})

	Context("concurrent requests", func() {
		It("creates exactly one session when concurrent server-generated requests share an idempotency_key", func() {
			const workers = 8
			results := make([]*db.Session, workers)
			errs := make([]error, workers)
			start := make(chan struct{})
			var wg sync.WaitGroup

			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					<-start
					results[idx], errs[idx] = Create(ctx, dbConn, CreateSessionAttr{
						ProfileID:      profile.ID,
						IdempotencyKey: "race-server-key",
						WorkoutType:    "wod",
						WODDescription: "Fran",
					})
				}(i)
			}

			close(start)
			wg.Wait()

			for i := 0; i < workers; i++ {
				Expect(errs[i]).NotTo(HaveOccurred(), "worker %d error", i)
				Expect(results[i].SessionID).NotTo(BeEmpty())
				Expect(results[i].ID).To(Equal(results[0].ID))
				Expect(results[i].SessionID).To(Equal(results[0].SessionID))
			}

			var count int64
			Expect(dbConn.Model(&db.Session{}).Where("profile_id = ?", profile.ID).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(1)))
		})

		It("creates exactly one session when concurrent requests share the same client session_id and idempotency_key", func() {
			const (
				workers = 8
				sid     = "WOD-20260407-01ARZ3NDEKTSV4RRFFQ69G5FAV"
			)
			results := make([]*db.Session, workers)
			errs := make([]error, workers)
			start := make(chan struct{})
			var wg sync.WaitGroup

			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					<-start
					results[idx], errs[idx] = Create(ctx, dbConn, CreateSessionAttr{
						ProfileID:      profile.ID,
						SessionID:      sid,
						IdempotencyKey: "race-client-key",
					})
				}(i)
			}

			close(start)
			wg.Wait()

			for i := 0; i < workers; i++ {
				Expect(errs[i]).NotTo(HaveOccurred(), "worker %d error", i)
				Expect(results[i].ID).To(Equal(results[0].ID))
				Expect(results[i].SessionID).To(Equal(sid))
			}

			var count int64
			Expect(dbConn.Model(&db.Session{}).Where("profile_id = ?", profile.ID).Count(&count).Error).NotTo(HaveOccurred())
			Expect(count).To(Equal(int64(1)))
		})
	})
})
