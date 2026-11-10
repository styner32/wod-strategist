package db_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/db"
)

var _ = Describe("TokenUsage scalar extras", func() {
	It("synchronizes TokenUsage scalar columns via BeforeSave", func() {
		u := db.TokenUsage{
			UsageMetadata: db.NullableJSONDocument(`{"promptTokenCount":100,"candidatesTokenCount":40,"thoughtsTokenCount":25,"toolUsePromptTokenCount":10,"cachedContentTokenCount":30,"totalTokenCount":175}`),
		}
		u.SyncUsageExtras()
		Expect(u.ThinkingTokens).To(Equal(int64(25)))
		Expect(u.ToolUseTokens).To(Equal(int64(10)))
		Expect(u.CachedTokens).To(Equal(int64(30)))
		Expect(u.UnmeasuredCalls).To(Equal(int16(0)))

		incomplete := db.TokenUsage{
			UsageMetadata: db.NullableJSONDocument(`{}`),
		}
		incomplete.SyncUsageExtras()
		Expect(incomplete.UnmeasuredCalls).To(Equal(int16(1)))
	})
})
