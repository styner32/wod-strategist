# Markdown 문서 일관성 리뷰

검토일: 2026-09-06 (KST). 범위: 저장소의 기존 Markdown 33개(숨김 `.agent/skills/`, `.jules/` 포함). 의존성·빌드 산출물은 제외했다. 검토 전부터 미추적 상태였던 `polar-sensor-pipeline-plan.md`도 포함했다.

문서 간 충돌을 찾고 관련 코드·마이그레이션·설정·테스트 소스와 대조했다. 이 작업에서 만든 변경은 문서에 한정한다. 최종 점검 때 별도 작업의 `merge_chunks.go`/`merge_chunks_test.go` 변경이 들어왔으며, 해당 코드는 수정하거나 되돌리지 않고 Phase 0.1 상태에 반영했다. 실제 기기, 배포 환경, 외부 서비스 요금/프로토콜을 검증하거나 개선 계획의 완료 조건을 재인증한 것은 아니다.

## 읽는 기준

- 현재 규칙: 루트/영역별 `AGENTS.md`. 현재 구현: 관련 코드와 `docs/agent-memory/`를 함께 확인한다.
- 목표 설계: `docs/video-analysis-improvement/`의 단계별 계약. [진행 상태 표](video-analysis-improvement/README.md#current-status-versus-target-work)에서 이미 들어간 구현과 남은 완료 조건을 구분한다.
- 과거 기록: `01-current-pipeline.md`, 루트 `plan.md`, `.jules/sentinel.md`. 과거 결함 설명을 현재 구현으로 읽지 않는다.
- `.agent/skills/`의 일반 예시에는 repository 계층, 새 의존성, CI 도입, 하드코딩 UI 등이 포함된다. 프로젝트 적용 시 영역별 규칙을 우선한다. 예시 자체를 프로젝트 구현으로 바꾸지는 않았다.

## 정리한 불일치

우선순위는 문서의 잘못된 지시를 따랐을 때의 영향 기준이다. “정리”는 문서를 고쳤다는 뜻이며 기능 수정 완료를 뜻하지 않는다.

| 우선순위 | 항목 | 충돌과 정리 내용 | 근거 |
|---|---|---|---|
| P0 | 영상 시간축 | API 지침/REL-03은 capture 시간 사용을 허용하고 다른 문서는 금지했다. 검증된 media 구간만 사용하고, legacy는 실제 파일 재구성/인덱싱으로 처리하도록 통일했다. | [저장 계약](agent-memory/storage-and-session-format.md#capture-time-versus-media-time), [buildSegmentsFromChunks](../api/internal/worker/video_analysis.go) |
| P0 | Polar 시간 정렬 | ms/초 변환 누락, pause만 빼는 영상 매핑, 모든 offset을 `Date.now()`로 계산한다는 설명을 수정했다. ACC 앵커와 HR 수신 시간, 미촬영 구간을 구분했다. | [Polar 계획](polar-sensor-pipeline-plan.md), [촬영 화면](../app/workout/visionTestPage.tsx), [merge](../api/internal/worker/merge_chunks.go) |
| P0 | 센서 저장/merge | 새 최상위 `sensors/` 제안을 세션 내부 하위 경로로 바꿨다. 정상 DB 경로의 실제 실패는 센서 파일에 대응하는 분석 행을 기다리는 재시도임을 명시했다. 최종 점검에서는 별도 작업의 필터/테스트 추가를 확인했으며 실행·배포 검증은 남아 있다. | [루트 규칙](../AGENTS.md), [listOriginalChunks](../api/internal/worker/merge_chunks.go) |
| P1 | 계획과 구현 상태 | media 컬럼, 누적 split 시간, exact task 등록, GCS 존재 필터, highlight v2가 이미 있는데 계획은 모두 미구현처럼 보였다. 날짜·진행 표·단계별 주석을 추가했다. | [개선 계획](video-analysis-improvement/README.md), [마이그레이션 000038](../api/internal/db/migrations/000038_create_chunk_reanalysis_runs.up.sql) |
| P1 | 청크 식별키 | recorded와 synthetic의 같은 index가 충돌하는 목표 스키마를 `(profile_id, session_id, chunk_source, chunk_index)`로 통일했다. manifest에는 recorded만 사용한다. 기존 컬럼 재생성/후속 rollback 삭제도 금지했다. | [REL-02/04](video-analysis-improvement/02-phase-1-integrity-and-idempotency.md), [COST-03](video-analysis-improvement/05-phase-4-cost-and-throughput.md) |
| P1 | 재분석과 적용 | “결과를 절대 변경하지 않음”, “POST는 request ID만 받음”을 현재 DTO에 맞췄다. 후보 생성·feedback·명시적 session apply를 구분했다. | [DTO](../api/internal/controllers/session_reanalysis_dto.go), [apply](../api/internal/controllers/session_reanalysis_handlers.go), [feedback 문서](agent-memory/analysis-feedback.md) |
| P1 | 인증 폐기/이름 재사용 | 다중 인스턴스에서도 즉시 토큰 폐기된다는 보장을 제거했다. 활성 사용자만 대상으로 한 unique index가 삭제된 이름의 재사용을 막는다는 반대 설명도 수정했다. | [인증 서비스](../api/internal/auth/service.go), [사용자 인덱스](../api/internal/db/migrations/000021_create_users.up.sql) |
| P1 | Gemini 파일 수명 | “항상 마지막에 삭제”, “모든 테스트는 5요청”을 제거했다. 성공 시 보존하는 two-pass 경로와 삭제를 소유한 경로를 구분했다. | [영상 분석](agent-memory/video-analysis.md#file-lifecycle), [테스트 안내](agent-memory/backend-testing.md) |
| P1 | 타임아웃 산술 | 동시 처리 10개를 누락한 “27분 영상 한계”를 삭제했다. `ceil(청크 수 / 10) × 청크 처리시간`도 split 단계의 예시이며 전체 시간 보장이 아님을 명시했다. | [split 구현](../api/internal/worker/split_video.go), [수정 문서](agent-memory/video-analysis.md#timeout-risk) |
| P1 | 테스트 DB 명령 | 새 migration 적용과 down/up 검증을 혼동한 안내를 수정했다. 일반 적용은 `migrate-test-up`; `redo`는 이미 적용된 1개를 되돌린다는 점을 명시했다. | [Makefile](../api/Makefile), [migration 규칙](agent-memory/migrations.md) |
| P1 | 피로도 산식/입력 | intensity 미제공 fallback, 양수 BPM 보너스 조건, volume 대체값, 반올림을 반영했다. 실제 전략/히스토리 호출은 `(score, nil, 0)`이므로 센서/청크 데이터가 이미 연결된 것으로 표현하지 않는다. | [피로 계산](../api/internal/fatigue/fatigue.go), [전략 호출](../api/internal/controllers/strategy_handlers.go), [히스토리 호출](../api/internal/controllers/highlight_response.go) |
| P1 | 점수 계약 | 예시 점수 `74`를 `68×0.5 + 82×0.3 + 72×0.2 = 73`으로 수정했다. recovery 공식과 잔여 0.3 배분이 미정인 skill 공식도 분리했다. | [점수 문서](agent-memory/historical-analysis.md), [현재 프롬프트](../api/internal/worker/history.go) |
| P1 | Polar 수명주기/파일 | 녹화 전 연결된 장치 전달, 재연결 앵커, 갭 시작/종료, 측정 불가 손실 수 `null`, 중복 stop, 고정 소유 프로필을 명시했다. 기기 미연결인데 메타가 기기/실제 설정을 안다고 가정한 예시를 요청 설정+후속 이벤트로 바꿨다. | [Polar Phase 1–4](polar-sensor-pipeline-plan.md) |
| P2 | 실행 안내 | 서버 기본 8080과 클라이언트/프록시 8088을 구분하고 로컬 예시에 override를 명시했다. API 명령의 작업 디렉터리, multipart profile, root의 admin 명령을 수정했다. | [루트 README](../README.md), [API README](../api/README.md), [설정](../api/internal/config/config.go) |
| P2 | 웹/CLI 안내 | 웹 README의 기본 Vite 템플릿을 실제 서비스 안내로 교체했다. JWT 없이 호출하는 legacy CLI와 동작하지 않는 Make 옵션을 정상 QA 경로처럼 소개하지 않도록 했다. | [웹 README](../web/README.md), [legacy 스크립트](../scripts/test-chunk-upload.js), [Vite 설정](../web/vite.config.ts) |
| P2 | ID/타입/참조 | WOD 전용 ID 설명을 4개 운동 유형으로 확장하고 legacy 예외를 유지했다. 옮겨진 dev handler/session label 경로, media SQL 타입, 자동 생성 타입이 모든 계약을 보증한다는 설명을 수정했다. | [ID 생성](../features/wod/workoutType.ts), [저장 문서](agent-memory/storage-and-session-format.md), [README](../README.md) |
| P2 | 모델/비용 | worker 기본 모델과 bare client 기본 모델, 전역 thinking과 단계별 설정을 구분했다. 저장소 비용 상수를 실시간 벤더 요금으로 표현하지 않고, 누적 비용 API에는 breakdown 배열이 없음을 반영했다. | [비용 코드](../api/internal/cost/cost.go), [비용 API](../api/internal/controllers/cost_handlers.go), [COST 계획](video-analysis-improvement/05-phase-4-cost-and-throughput.md) |
| P2 | 완료된 과거 작업 | 이미 일반 오류 문구로 바뀐 split 작업을 루트의 활성 `plan.md`에서 과거 작업 기록으로 바꾸고 현행 개선 계획으로 연결했다. | [plan.md](../plan.md), [split 실패 처리](../api/internal/worker/split_video.go) |

## 남겨 둔 구현·제품 결정

1. **영상 무결성:** final partial 청크 업로드/manifest, full-result 재시도 멱등성, `MAX(end_secs)` 대신 media coverage 검증은 REL-01/02/06/07에서 해결해야 한다.
2. **임의 프로필 기본값:** `lookupProfileString`에는 고정 생년월일·신체 정보 fallback이 실제로 남아 있다. 새 appearance 기능이 이 결함까지 해결했다고 표시하지 않았다. ACC-02 작업이 필요하다.
3. **전체 재분석 apply의 파생 결과:** 현재 apply는 분석 텍스트·점수·highlight 정의 등을 바꾸지만 기존 highlight 영상/hardsub 등의 재생성·무효화는 수행하지 않는다. 파생 결과 일치 정책이 필요하다.
4. **평가/비용:** web compare 라벨과 worker 동작, 생각 토큰 비용 누락, 단계별 모델 설정, vendor 가격/지원 확인은 남은 작업이다. 이번 리뷰의 저장소 상수 확인을 실제 청구 검증으로 사용하면 안 된다.
5. **Polar:** 공식 PMD 명세 및 H10 덤프 검증, 센서→프레임 정렬 오차, BLE 종료 timeout, 실제 크기·성능, 수집 후 소비자/보존 정책이 필요하다. “lifecycle로 삭제됨”이라는 오류 문구만으로 배포 버킷 정책을 추정하지 않는다.
6. **점수:** skill WOD의 잔여 0.3 가중치와 데이터 없는 피로 fallback의 제품 의미를 결정해야 한다. 시각적 피로 증거와 heuristic readiness 점수는 서로 다른 계약이다.
7. **일반 예시와 과거 보안 기록:** `.agent/skills/`의 예시는 프로젝트 규칙 적용 전 참고 자료로 유지했다. `.jules/sentinel.md`의 `filepath.Base` 권고만으로 입력 검증을 대체하거나 제거된 API-key 인증을 복원하지 않는다.

## 변경 파일

- 루트/앱 지침: [AGENTS.md](../AGENTS.md), [api/AGENTS.md](../api/AGENTS.md), [app/AGENTS.md](../app/AGENTS.md).
- 실행/이력: [README.md](../README.md), [api/README.md](../api/README.md), [web/README.md](../web/README.md), [plan.md](../plan.md).
- 도메인 문서: [analysis-feedback](agent-memory/analysis-feedback.md), [auth](agent-memory/auth.md), [backend-testing](agent-memory/backend-testing.md), [fatigue-and-strategy](agent-memory/fatigue-and-strategy.md), [fitness-level](agent-memory/fitness-level.md), [historical-analysis](agent-memory/historical-analysis.md), [migrations](agent-memory/migrations.md), [storage-and-session-format](agent-memory/storage-and-session-format.md), [video-analysis](agent-memory/video-analysis.md).
- 개선 계획: [README](video-analysis-improvement/README.md), [01](video-analysis-improvement/01-current-pipeline.md), [02](video-analysis-improvement/02-phase-1-integrity-and-idempotency.md), [03](video-analysis-improvement/03-phase-2-observability-and-evaluation.md), [04](video-analysis-improvement/04-phase-3-accuracy-and-contracts.md), [05](video-analysis-improvement/05-phase-4-cost-and-throughput.md), [06](video-analysis-improvement/06-test-matrix-and-rollout.md).
- 센서/리뷰: [Polar 계획](polar-sensor-pipeline-plan.md), 이 리뷰 문서.

## 검증

- 문서 수집/본문·코드 검색: `rg --files --hidden -g '*.md'`에 의존성/빌드 제외 조건 적용, `rg -n`, 관련 파일 읽기.
- `git diff --check`: 통과.
- `python3 /tmp/check_wod_docs.py`: 최종 문서 34개, 상대 링크 153개(로컬 Markdown heading 앵커 포함), 프로젝트 JSON/JSONL 예시 7개 검사 통과. 외부 URL의 응답/내용은 검증하지 않았다.
- 이 문서 작업에서는 앱/API 코드 및 테스트를 추가·수정하지 않았고 앱/Go 테스트도 실행하지 않았다. 별도 작업으로 들어온 merge 필터 테스트의 실행 결과를 대신 보증하지 않는다. 문서에 적힌 테스트 명령을 실행 완료로 간주하지 않는다.
