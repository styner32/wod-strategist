package storage_test

import (
	"context"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/wod-strategist/api/internal/storage"
	"github.com/wod-strategist/api/internal/testhelpers"
	"google.golang.org/api/option"
)

var _ = Describe("Storage audit classification and listing", func() {
	DescribeTable("classifies GCS object paths into audit categories",
		func(objectName string, want storage.ObjectCategory) {
			Expect(storage.ClassifyObjectCategory(objectName)).To(Equal(want))
		},
		Entry("raw chunk upload", "videos/10/WOD-20261106-01ABC/chunk_0001.mp4", storage.CategorySource),
		Entry("prepared chunk", "videos/10/WOD-20261106-01ABC/prepared/v1/chunk_0001.mp4", storage.CategoryPrepared),
		Entry("merge staging partial", "videos/10/WOD-20261106-01ABC/merge_staging/part_01.mp4", storage.CategoryMergeStaging),
		Entry("merged playback", "videos/10/WOD-20261106-01ABC/merged.mp4", storage.CategoryPlayback),
		Entry("highlight reel", "videos/10/WOD-20261106-01ABC/highlights/reel_1.mp4", storage.CategoryPlayback),
		Entry("hardsub video", "videos/10/WOD-20261106-01ABC/merged_hardsub.mp4", storage.CategoryHardsub),
		Entry("tracking frame", "videos/10/WOD-20261106-01ABC/tracking/37737.jpg", storage.CategoryOther),
		Entry("non-session prefix", "stretches/1.mp4", storage.CategoryOther),
	)

	It("lists object sizes via ListSizedObjectInfos without mutating or deleting objects", func() {
		transport := testhelpers.NewMockTransport()
		transport.New("https://storage.googleapis.com").
			Get("/storage/v1/b/test-bucket/o?alt=json&prefix=videos%2F10%2F&prettyPrint=false&projection=full").
			Reply(http.StatusOK).
			JSON(map[string]any{
				"items": []any{
					map[string]any{"name": "videos/10/WOD-20261106-01ABC/chunk_0001.mp4", "generation": "11", "size": "1048576"},
					map[string]any{"name": "videos/10/WOD-20261106-01ABC/merged.mp4", "generation": "12", "size": "4194304"},
				},
			})

		client, err := storage.NewClient(
			context.Background(),
			"test-bucket",
			option.WithHTTPClient(&http.Client{Transport: transport}),
			option.WithoutAuthentication(),
		)
		Expect(err).NotTo(HaveOccurred())

		items, err := client.ListSizedObjectInfos(context.Background(), "videos/10/")
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(2))
		Expect(items[0].Size).To(Equal(int64(1048576)))
		Expect(items[1].Size).To(Equal(int64(4194304)))
		Expect(transport.Verify()).To(Succeed())
	})
})
