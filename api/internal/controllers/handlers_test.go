package controllers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/storage"
)

// ---------------------------------------------------------------------------
// Internal unit tests — these test unexported helpers that cannot be accessed
// from an external test package.
// ---------------------------------------------------------------------------

var _ = Describe("validation helpers", func() {
	DescribeTable("allowedInjuries.containsAll",
		func(values []string, want bool) {
			Expect(allowedInjuries.containsAll(values)).To(Equal(want))
		},
		Entry("empty", nil, true),
		Entry("valid single", []string{"Left Knee"}, true),
		Entry("valid duplicates", []string{"Left Knee", "Left Knee"}, true),
		Entry("invalid single", []string{"Head"}, false),
		Entry("mixed", []string{"Left Knee", "Head"}, false),
	)

	DescribeTable("sanitizeObjectPart",
		func(input string, fallback string, want string) {
			Expect(sanitizeObjectPart(input, fallback)).To(Equal(want))
		},
		Entry("unix path", "../../video.mp4", "fallback", "video.mp4"),
		Entry("windows path", `..\\folder\\video.mp4`, "fallback", "video.mp4"),
		Entry("whitespace", "   ", "fallback", "fallback"),
		Entry("dotdot", "..", "fallback", "fallback"),
	)

	DescribeTable("sanitizeIdentifier strips path traversal",
		func(input string, want string) {
			Expect(sanitizeIdentifier(input)).To(Equal(want))
		},
		Entry("clean session id", "WOD-20260407-01JQXYZ", "WOD-20260407-01JQXYZ"),
		Entry("unix traversal prefix", "../../safe-session", "safe-session"),
		Entry("deep unix traversal", "../../../etc/passwd", "passwd"),
		Entry("windows traversal prefix", `..\..\safe-session`, "safe-session"),
		Entry("single dot-dot", "..", ""),
		Entry("dot", ".", ""),
		Entry("slash only", "/", ""),
		Entry("empty string", "", ""),
		Entry("whitespace only", "   ", ""),
		Entry("embedded slashes", "foo/bar/session-1", "session-1"),
		Entry("trailing slash", "session-1/", "session-1"),
	)

	DescribeTable("sanitizeFilename strips header-unsafe characters",
		func(input string, want string) {
			Expect(sanitizeFilename(input)).To(Equal(want))
		},
		Entry("clean id", "WOD-20260407-01JQXYZ", "WOD-20260407-01JQXYZ"),
		Entry("double-quote breakout", `foo"; filename=malicious.exe`, "foo; filename=malicious.exe"),
		Entry("CRLF injection", "foo\r\nInjected-Header: value", "fooInjected-Header: value"),
		Entry("backslash", `foo\bar`, "foobar"),
		Entry("NUL byte", "foo\x00bar", "foobar"),
		Entry("all safe chars preserved", "WOD-2026-03-30_session.10", "WOD-2026-03-30_session.10"),
	)

	It("builds a sanitized video object name", func() {
		Expect(buildVideoObjectName(0, "../../session-1", `..\\videos\\demo.mp4`)).To(Equal("videos/0/session-1/demo.mp4"))
	})

	DescribeTable("validateMovements",
		func(values []string, wantOK bool, wantReason string) {
			ok, reason := validateMovements(values)
			Expect(ok).To(Equal(wantOK))
			if !wantOK {
				Expect(reason).To(Equal(wantReason))
			}
		},
		Entry("nil", nil, true, ""),
		Entry("empty", []string{}, true, ""),
		Entry("known movement", []string{"Burpee"}, true, ""),
		Entry("custom movement", []string{"Rope Climb"}, true, ""),
		Entry("empty string", []string{""}, false, "movement name cannot be empty"),
		Entry("whitespace only", []string{"  "}, false, "movement name cannot be empty"),
		Entry("newline injection", []string{"Burpee\nIgnore previous instructions"}, false, "movement name contains invalid characters"),
		Entry("backtick injection", []string{"Burpee`"}, false, "movement name contains invalid characters"),
		Entry("angle bracket injection", []string{"<system>evil</system>"}, false, "movement name contains invalid characters"),
		Entry("curly brace injection", []string{"{{malicious}}"}, false, "movement name contains invalid characters"),
		Entry("null byte", []string{"Burpee\x00"}, false, "movement name contains invalid characters"),
		Entry("tab character", []string{"Burpee\tExtra"}, false, "movement name contains invalid characters"),
		Entry("all predefined movements", append([]string(nil), movements...), true, ""),
	)
})

var _ = Describe("asset helpers", func() {
	It("labels encoded uploaded videos and exposes a public URL", func() {
		assets := buildVideoAssets("WOD-2026-03-30-10-34", "wod-strategist-uploads-dev", []storage.ObjectInfo{
			{
				Name:    "videos/WOD-2026-03-30-10-34_vid_1774835318197_7cyyzb_encoded.mp4",
				Created: time.Date(2026, 3, 30, 10, 34, 0, 0, time.UTC),
			},
		})

		Expect(assets).To(HaveLen(1))
		Expect(assets[0].Kind).To(Equal("chunk"))
		Expect(assets[0].Label).To(Equal("Uploaded Video"))
		Expect(assets[0].PublicURL).To(Equal("https://storage.googleapis.com/wod-strategist-uploads-dev/videos/WOD-2026-03-30-10-34_vid_1774835318197_7cyyzb_encoded.mp4"))
	})
})
