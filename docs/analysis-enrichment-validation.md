# 분석 요약·Agentic Highlights 구현 검증 (2026-09-28)

## 구현

- `api/internal/db/migrations/000052_add_analysis_enrichment.{up,down}.sql`: 두 JSONB 컬럼, 기존 행 기본값 `{}`, NOT NULL.
- `api/internal/worker/analysis_enrichment.go`: 저장과 같은 트랜잭션의 outbox, 요약/영상 준비/구간별 생성/정리 작업, 30초 복구, fingerprint 및 실행 ID 검사, 마지막 성공 결과 보존.
- `api/internal/gemini/highlight_agentic.go`, `client.go`: HIGH/LOW streaming, 생성 재시도 없음, 업로드 소유권 즉시 기록, 사고 텍스트 제외.
- `api/internal/controllers/analysis_enrichment_handlers.go`, router 및 기존 완료/적용/검증 경로: 소유권 검증 API, 자동 연결, 읽기와 유료 실행 분리.
- `web/src/history/components/AnalysisOverview.tsx`: 전체 요약 → 하이라이트 단일 펼침 → 기존/추가 관찰 및 관련 세그먼트. 시각 버튼으로 기존 플레이어 이동.
- `AnalysisMarkdown.tsx`, `analysisPresentation.ts`, `analysisMarkdown.css`: Markdown 서식, 알려진 내부 데이터의 표시 분리, 저장 원문 접근, 코드 내부 가짜 세그먼트 제목 제외.
- 세션 상세/목록/재분석 비교 및 API 타입 갱신. `react-markdown`, `remark-gfm` 추가; 기존 패키지 버전 변경 없음.
- API·worker·Terraform의 `ENABLE_AGENTIC_HIGHLIGHTS` 기본값 false.

## 검증

분리된 PostgreSQL `127.0.0.1:55481/wod_test`, Redis `127.0.0.1:6391` DB 15를 사용했다. Gemini/GCS 요청은 MockTransport로 대체했다.

- `go test -p 1 ./internal/gemini ./internal/worker ./internal/controllers ./internal/config`: 통과.
- 최종 트랜잭션 변경 후 `go test -p 1 ./internal/worker ./internal/controllers`: 통과.
- `go test -p 1 ./internal/worker -ginkgo.focus='Analysis enrichment lifecycle'`: outbox rollback 및 미발행 예약 복구 포함 통과.
- `go build ./cmd/server ./cmd/worker`: 통과.
- `npm --prefix web run build`: 통과.
- `node node_modules/jest/bin/jest.js --runInBand web/test/analysis-presentation.test.ts web/test/highlights.test.ts --silent`: 18개 통과.
- migration 52 up → down 1 → up: 통과. 두 컬럼 모두 JSONB, NOT NULL, 기본값 `'{}'::jsonb`, 기존 행 NULL 0건.
- `terraform fmt -check infra/compute.tf infra/variables.tf`, `git diff --check`: 통과.

신규 Ginkgo 테스트는 소유권·GET 무부작용·중복 실행 ID·동일 Files URI·각 구간 독립 호출·HIGH/LOW·부분 실패·전체 실패·마지막 성공 보존·원본 변경 차단·중단 처리·소유 업로드 정리·outbox 원자성을 확인한다. 기존 스트리밍 테스트는 timeout, 빈 응답, 잘못된 응답, 도구 제한 종료와 nullable 사용량을 확인한다.

실제 React 컴포넌트에 검증용 데이터를 넣은 로컬 브라우저에서 390px/1440px 반응형, 굵게/제목/목록/인용문/표, 내부 JSON 비노출, 단일 펼침, Enter 키, 영상 자동 이동 없음, 근거 시각 83초 이동을 확인했다. 페이지 가로 넘침은 없었다. 임시 검증 페이지는 제거했다. 실사용 세션의 모델 관찰 정확도를 검증한 것은 아니다.

## 제한 및 적용 상태

- ESLint는 기존 TypeScript 7/TypeScript-ESLint 조합에서 `Cannot read properties of undefined (reading 'Cjs')`로 실행되지 않는다. 관련 설정이나 기존 패키지 버전은 수정하지 않았다.
- 웹 빌드는 기존 단일 번들의 500KB 경고를 출력한다.
- 운영 DB migration, API·worker·웹 배포와 기능 활성화는 수행하지 않았다. 배포 시 migration → API/worker/web → 점검 → 플래그 활성화 순서를 따른다.
- 실제 유료 호출과 기존 세션 일괄 분석은 수행하지 않았다. 전체 영상 접근을 시간 프롬프트만으로 강제 제한하지 않으며, 도구 호출 확인과 판독 정확성은 별개다.
