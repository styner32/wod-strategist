package main

import (
	"context"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/testhelpers"
)

var _ = Describe("Live replay login", func() {
	It("uses the real login endpoint and authorizes profile reads without persisting credentials", func() {
		dir, _, _ := runTestFixture(false)
		clock := &runTestClock{now: time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)}
		opts := runTestOptions(dir, clock)
		opts.Token, opts.Username, opts.Password = "", "replay-owner", "private-password"
		transport := testhelpers.NewMockTransport()
		transport.New(runTestAPI).Post("/api/v1/auth/login").MatchBodyContains(`"username":"replay-owner"`).MatchBodyContains(`"password":"private-password"`).Reply(200).JSON(map[string]string{"token": "issued-private-token"})
		transport.New(runTestAPI).Get("/api/v1/profiles/10").MatchHeader("Authorization", "Bearer issued-private-token").Reply(403)
		err := runReplay(context.Background(), opts, &http.Client{Transport: transport}, io.Discard)
		Expect(err).To(MatchError(ContainSubstring("HTTP 403")))
		Expect(transport.Verify()).To(Succeed())
		_, data := runTestReport(opts)
		Expect(string(data)).NotTo(ContainSubstring("private-password"))
		Expect(string(data)).NotTo(ContainSubstring("issued-private-token"))
		for _, request := range transport.Requests() {
			if request.Method == http.MethodPost {
				Expect(request.Headers.Get("Authorization")).To(BeEmpty())
			}
		}
	})
})
