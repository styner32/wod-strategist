package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Benchmark fixture catalog and selection", func() {
	DescribeTable("parses human workout timestamps and seconds",
		func(input string, expected float64) {
			got, err := parseMediaTimestamp(input)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeNumerically("~", expected, 1e-6))
		},
		Entry("short MM:SS", "4:50", 290.0),
		Entry("padded MM:SS", "04:50", 290.0),
		Entry("fractional MM:SS", "11:54.7", 714.7),
		Entry("HH:MM:SS", "0:04:50.5", 290.5),
		Entry("plain seconds", "290", 290.0),
		Entry("fractional seconds", "714.7", 714.7),
		Entry("go duration", "4m50s", 290.0),
	)

	DescribeTable("rejects malformed or out-of-range timestamps",
		func(input string) {
			_, err := parseMediaTimestamp(input)
			Expect(err).To(HaveOccurred())
		},
		Entry("empty", ""),
		Entry("seconds >= 60", "4:60"),
		Entry("minutes >= 60 with hours", "1:60:00"),
		Entry("negative", "-10"),
		Entry("too many colons", "0:1:2:3"),
		Entry("non-numeric", "four-fifty"),
	)

	It("parses automatic and explicit end timestamps", func() {
		for _, autoInput := range []string{"", "auto", "AUTO", "end"} {
			secs, auto, err := parseEndTimestamp(autoInput)
			Expect(err).NotTo(HaveOccurred())
			Expect(auto).To(BeTrue())
			Expect(secs).To(Equal(0.0))
		}
		secs, auto, err := parseEndTimestamp("9:50")
		Expect(err).NotTo(HaveOccurred())
		Expect(auto).To(BeFalse())
		Expect(secs).To(Equal(590.0))
	})

	DescribeTable("derives safe fixture names from WOD descriptions or session IDs",
		func(wodDescription, sessionID, expected string) {
			Expect(deriveFixtureName(wodDescription, sessionID)).To(Equal(expected))
		},
		Entry("quoted Diane", "\"Diane\"\n21-15-9\nDeadlifts\nHandstand Push-ups", "WOD-20261028-01ABC", "diane"),
		Entry("plain Grace", "Grace\nFor Time:\n30 Clean and Jerks", "WOD-20260930-01ABC", "grace"),
		Entry("title with parens", "Fran (21-15-9 Thrusters & Pull-ups)", "WOD-20261101-01ABC", "fran"),
		Entry("fallback to session ID", "", "WOD-20261028-01ABC", "wod-20261028-01abc"),
	)

	DescribeTable("rejects unsafe fixture names",
		func(raw string) {
			_, err := normalizeFixtureName(raw)
			Expect(err).To(HaveOccurred())
		},
		Entry("empty", " "),
		Entry("leading dash", "-diane"),
		Entry("path separator", "a/b"),
		Entry("traversal", ".."),
	)

	It("computes automatic end time within video, mapping, and Polar footer bounds", func() {
		mapping := []MappingSegment{
			{MediaStartMs: 0, MediaEndMs: 710000, CaptureStartMs: 440.753, CaptureEndMs: 710187.445160},
			{MediaStartMs: 710000, MediaEndMs: 714733.331, CaptureStartMs: 710187.445160, CaptureEndMs: 714920.776160},
		}
		// Sensor footer ends ~31ms before the final video frame; auto-end snaps down to 714.7s (12,741 frames at 30 FPS).
		endSecs, err := resolveAutoMediaEndSecs(mapping, 290.0, 714.733333, 30.0, 714890.0)
		Expect(err).NotTo(HaveOccurred())
		Expect(endSecs).To(Equal(714.7))
		captureEnd, err := prepareCaptureEnd(mapping, endSecs*1000)
		Expect(err).NotTo(HaveOccurred())
		Expect(captureEnd).To(BeNumerically("<=", 714890.0))

		// Video-capped when sensor runs longer than video.
		videoCapped, err := resolveAutoMediaEndSecs(mapping, 290.0, 600.0, 30.0, 800000.0)
		Expect(err).NotTo(HaveOccurred())
		Expect(videoCapped).To(Equal(600.0))

		// Rejects start past sensor footer.
		_, err = resolveAutoMediaEndSecs(mapping, 714.72, 714.733333, 30.0, 714890.0)
		Expect(err).To(HaveOccurred())
	})

	It("discovers prepared fixtures, lists them, and resolves direct or interactive choices safely", func() {
		root := GinkgoT().TempDir()
		raw := validationTelemetry()

		diane := validationManifest(raw)
		diane.Name = "diane"
		diane.SourceSessionID = "WOD-20261028-DIANE"
		diane.WODDescription = "\"Diane\"\n21-15-9"
		diane.Movements = []string{"Deadlift", "Handstand Push-up"}
		diane.MediaStartSecs, diane.MediaEndSecs = 290, 714.7

		grace := validationManifest(raw)
		grace.Name = "grace"
		grace.SourceSessionID = "WOD-20260930-GRACE"
		grace.WODDescription = "Grace\nFor Time"
		grace.Movements = []string{"Clean and Jerk"}
		grace.MediaStartSecs, grace.MediaEndSecs = 290, 590

		for _, item := range []struct {
			slug string
			m    FixtureManifest
		}{
			{"diane", diane},
			{"grace", grace},
		} {
			dir := filepath.Join(root, item.slug)
			Expect(os.MkdirAll(dir, 0700)).To(Succeed())
			Expect(writeJSON(filepath.Join(dir, "manifest.json"), item.m)).To(Succeed())
		}

		var listOut bytes.Buffer
		Expect(commandIO([]string{"list", "--fixtures-dir=" + root}, nil, &listOut)).To(Succeed())
		Expect(listOut.String()).To(ContainSubstring("[1] diane"))
		Expect(listOut.String()).To(ContainSubstring("[2] grace"))
		Expect(listOut.String()).To(ContainSubstring("07:04.7"))
		Expect(listOut.String()).To(ContainSubstring("05:00.0"))

		// Direct resolution by --wod name, --fixture bare name, catalog index, explicit path, and source session ID.
		got, err := resolveFixtureSelection(root, "grace", "", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(filepath.Join(root, "grace")))

		got, err = resolveFixtureSelection(root, "", "diane", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(filepath.Join(root, "diane")))

		got, err = resolveFixtureSelection(root, "", "2", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(filepath.Join(root, "grace")))

		got, err = resolveFixtureSelection(root, "", filepath.Join(root, "diane"), nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(filepath.Join(root, "diane")))

		got, err = resolveFixtureSelection(root, "WOD-20261028-DIANE", "", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(filepath.Join(root, "diane")))

		// Prepare target resolution treats bare --fixture names as catalog slugs and paths as directories.
		prepName, prepDir, err := resolvePrepareTarget("", "fran")
		Expect(err).NotTo(HaveOccurred())
		Expect(prepName).To(Equal("fran"))
		Expect(prepDir).To(BeEmpty())

		prepName, prepDir, err = resolvePrepareTarget("", "tmp/live-replay/custom-fran")
		Expect(err).NotTo(HaveOccurred())
		Expect(prepName).To(BeEmpty())
		Expect(prepDir).To(Equal("tmp/live-replay/custom-fran"))

		// Interactive selection by number and by name.
		var promptOut bytes.Buffer
		got, err = resolveFixtureSelection(root, "", "", strings.NewReader("2\n"), &promptOut)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(filepath.Join(root, "grace")))
		Expect(promptOut.String()).To(ContainSubstring("Select fixture"))

		got, err = resolveFixtureSelection(root, "", "", strings.NewReader("DIANE\n"), &bytes.Buffer{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(filepath.Join(root, "diane")))

		// Safe cancellation on quit, blank Enter, EOF, unknown name, and conflicting flags.
		_, err = resolveFixtureSelection(root, "", "", strings.NewReader("q\n"), &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("cancelled")))

		_, err = resolveFixtureSelection(root, "", "", strings.NewReader("\n"), &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("cancelled")))

		_, err = resolveFixtureSelection(root, "", "", strings.NewReader(""), &bytes.Buffer{})
		Expect(err).To(MatchError(ContainSubstring("pass WOD=")))

		_, err = resolveFixtureSelection(root, "fran", "", nil, nil)
		Expect(err).To(MatchError(ContainSubstring("available fixtures: diane, grace")))

		_, err = resolveFixtureSelection(root, "", filepath.Join(root, "missing"), nil, nil)
		Expect(err).To(MatchError(ContainSubstring("no manifest.json")))

		_, err = resolveFixtureSelection(root, "diane", "grace", nil, nil)
		Expect(err).To(MatchError(ContainSubstring("not both")))
	})
})
