# Live 영상·Polar H10 재생 테스트

큰 업데이트 뒤 수동으로 실행하는 운영 API 통합 점검이다. 기존 녹화의 입력을 새 `WOD-YYYYMMDD-ULID` 세션으로 보내며, **대상 profile은 10으로 고정하고 결과를 보존한다.** 실제 업로드·작업 큐·AI 분석을 실행하므로 분석 비용이 발생한다. 자동 CI나 정기 실행에 연결하지 않는다. 카메라·BLE 하드웨어 자체의 검증은 별도다.

## 최초 준비

- Go, `ffmpeg`, `ffprobe`가 필요하다. `prepare`에는 원본 DB 읽기 권한, GCS 읽기용 Application Default Credentials와 DB 접속 경로가 필요하다. Cloud SQL Proxy 접속은 [README의 DB 안내](../../README.md#cloud-sql-dev)를 참고한다.
- 준비된 벤치마크 fixture는 `api/tmp/live-replay/fixtures/<이름>/` 아래 이름별로 관리한다. 현재 로컬에 준비된 대표 벤치마크는 다음 두 가지다:
  - `diane`: profile **1**의 `WOD-20261028-01M602PXNAAZFKKNKXNSN2D61S`("Diane": 21-15-9 Deadlifts & Handstand Push-ups), `04:50..11:54.7`(`290.0..714.7초`, media `424.7초`, 43개 조각)
  - `grace`: profile **1**의 `WOD-20260930-01M3QZH8TPXYMBGP9ER5W6SMBB`("Grace": 30 Clean and Jerks), `04:50..09:50`(`290.0..590.0초`, media `300.0초`, 30개 조각)
- 저장된 원본을 읽는 실제 DB 이름은 `wod-strategist_dev`다. 오래된 `api/.env`의 로컬 `wod_dev`와 구별한다. `prepare`는 DB를 읽기 전용 트랜잭션으로 조회하고, 원본 파일은 변경하지 않는다.
- `api/.env.live.local`에 아래 설정을 저장한다. 이 파일과 `api/tmp/` 산출물은 Git에서 제외된다. 예시의 자리표시자는 실제 로컬 설정으로 바꾼다.

```dotenv
# prepare에만 필요. Proxy 포트는 README 예시 기준이며 실제 접속 설정과 맞춘다.
DATABASE_URL=postgresql://<DB_USER>:<DB_PASSWORD>@localhost:15432/wod-strategist_dev?sslmode=disable
GCS_BUCKET_NAME=<SOURCE_BUCKET>

# run 인증: profile 10을 소유한 사용자의 실제 로그인 정보
LIVE_TEST_USERNAME=<USERNAME>
LIVE_TEST_PASSWORD=<PASSWORD>
# 또는 로그인 대신 이미 발급된 토큰 사용: LIVE_TEST_JWT=<TOKEN>

# API 주소는 /api/v1까지 포함. 생략하면 루트 .env의 EXPO_PUBLIC_API_URL 사용.
# LIVE_TEST_API_URL=https://<LIVE_API_HOST>/api/v1
# 선택: 결과 보고서에 웹 화면 링크를 넣는 HTTPS 웹 origin
# LIVE_TEST_WEB_URL=https://<WEB_HOST>
```

`live-test-prepare`(`test-live-prepare`), `live-test`(`test-live`), `live-test-list`(`test-live-list`)는 Makefile의 기존 `.env` include/export를 생략한다. CLI가 `api/.env.live.local` → 루트 `.env` → `api/.env` 순으로 비어 있는 값을 채운다. **이미 설정된 프로세스 환경변수가 우선**하므로 이전에 export한 DB/API 주소가 있으면 먼저 확인한다. `run`과 `list`에는 DB·GCS 관리자 권한이 필요 없다. 토큰이 있으면 `LIVE_TEST_JWT`를 우선 사용하며, 없으면 username/password로 로그인하고 profile 10 소유권을 확인한다.

새 벤치마크 세션을 운동 시작 시각부터 잘라 준비할 때는 `SESSION`과 `START`(`MM:SS`, `HH:MM:SS` 또는 초)를 지정한다. `WOD`를 생략하면 DB의 `wod_description` 첫 줄에서 slug(`diane`, `grace` 등)를 자동 추출하며, `END`를 생략하면(`auto`) 영상 길이·매핑·Polar 센서 footer가 모두 겹치는 마지막 프레임 경계까지 자동으로 자른다.

```bash
# 시작 시각(4:50)부터 녹화 끝까지 자동 추출 → api/tmp/live-replay/fixtures/diane
make live-test-prepare WOD=diane SESSION=WOD-20261028-01M602PXNAAZFKKNKXNSN2D61S START=4:50

# 종료 시각을 직접 지정해 추출 → api/tmp/live-replay/fixtures/grace
make live-test-prepare WOD=grace SESSION=WOD-20260930-01M3QZH8TPXYMBGP9ER5W6SMBB START=4:50 END=9:50
```

각 fixture 출력은 `api/tmp/live-replay/fixtures/<이름>/`의 `manifest.json`, `sensor.ndjson`, `chunks/*.mp4`다. 영상·센서의 원본 object/generation/hash, 시간 매핑과 잘라낸 샘플 수를 manifest에 남긴다. 기존 fixture는 덮어쓰지 않으므로 다시 준비할 때는 새 `WOD` 이름이나 `--fixture` 경로를 지정한다.

이미 준비된 자료의 영상 분할만 다시 만들 때는 DB 없이 기존 manifest와 센서를 재사용한다(`--from-fixture`에 fixture 이름 또는 경로 지정). 원본 영상은 저장된 generation·SHA256으로 고정해 GCS에서 읽으며, 센서 파일은 그대로 보존한다.

```bash
make live-test-prepare WOD=diane-v2 ARGS='--from-fixture=diane'
```

## 목록 조회·선택 실행과 결과 확인

```bash
# 준비된 벤치마크 fixture 목록 확인 (이름·길이·구간·와드명·원본 세션)
make live-test-list

# 대화형 목록에서 번호/이름을 골라 실행 (q 또는 빈 입력 시 안전 취소)
make live-test

# 특정 벤치마크 이름으로 즉시 실행 (WOD, FIXTURE, ARGS='-fixture <이름>' 모두 동일하게 동작)
make live-test WOD=diane
make live-test FIXTURE=diane
make live-test WOD=grace ARGS='--analysis-timeout=45m'

# 전체 옵션과 기본값 확인
make live-test-prepare ARGS='-h'
make live-test ARGS='-h'
```

- 입력 재생은 선택한 fixture의 capture 길이를 그대로 따른다(`grace` 약 5분, `diane` 약 7분 5초). 각 조각의 capture 종료 시각까지 기다린 뒤 영상과 quality-filtered peak BPM을 전송한다. 마지막 조각 뒤 `calculation_version:3` 센서를 업로드하고, 완료된 timeline을 확인한 다음 merge를 요청한다. 이 순서는 센서 처리가 끝나기 전에 `activity:review`가 실행되는 경합을 막는다.
- 기본 polling 간격은 5초, 센서 완료 대기는 5분, merge 이후 분석 대기는 30분이다. `--poll-interval`, `--sensor-timeout`, `--analysis-timeout`, `--output-dir`로 조절한다.
- 결과는 `api/tmp/live-replay/runs/{새 session ID}/report.json`에 저장한다. 재식별한 `sensor.ndjson`과 확인용 `merged.mp4`도 같은 실행 폴더에 저장한다. 실패·중단 시 보고서와 이미 생성된 운영 세션을 보존한다. HTTP 요청을 자동 재전송하지 않으며, 명령을 다시 실행하면 새 세션을 만든다.
- `passed`는 업로드·분석 완료, 입력/결과의 session·profile·센서 version·샘플/시간 정합성, activity review, 병합 영상 길이 확인을 뜻한다. **동작 분류·반복 수·코칭 내용의 정확도는 보고서와 웹 화면에서 수동 검토한다.** 계정 권한, 서버 기능 설정, 분석 실패와 timeout은 해당 단계의 실패로 기록한다.
- 보고서의 `profile_before`, `profile_after`, 센서 `calculation_inputs`를 함께 확인한다. 보존된 테스트는 일반 profile 10 기록으로 취급된다. 유효 ACC가 120초 이상인 v3 완료 세션이 3개 이상 쌓이면 기존 worker가 개인 ENMO calibration을 자동 설정할 수 있으므로, 반복 결과가 이전과 항상 동일하다고 가정하지 않는다.

## 구현 시 지킬 계약

- 영상 시간과 센서 capture 시간을 혼용하지 않는다. 실제 추출한 기본 구간(`290.0..714.7초`)은 media **424.7초**(`capture_start_ms=290345.765`, `capture_end_ms=714887.445`, 센서 footer `714890ms` 이내)에 capture 약 **424.541680초**, HR **424개**, ACC **21,750개**다. DB의 전체 정밀도를 사용한 정확한 경계는 manifest로 확인한다. capture/media 매핑과 원래 ACC `dt`·sampling rate를 보존하고, 관측되지 않은 샘플을 보간하지 않는다.
- NDJSON의 ACC 배열은 샘플 단위로 자른다. 활성 device/stream 문맥을 0초에 복원하고 pause/gap·footer·identity·byte size/SHA256을 새 구간에 맞춘다. device nanosecond timestamp와 모르는 JSON 필드는 정확히 보존한다. 영상 조각은 오디오를 제외한다. AAC encoder padding이 worker가 사용하는 컨테이너 `format.duration`을 늘려 누적 시간 매핑을 어긋나게 하기 때문이다.
- 영상은 `setpts=PTS-STARTPTS`로 시작 시각을 맞추고 `segment_time_delta=1/(2*FPS)`를 적용한다. 분할 지점보다 미세하게 빠른 키프레임을 놓쳐 첫 조각이 18.333초가 되던 문제를 막는다. manifest의 `chunk_seconds`(이전 자료의 기본값 10)와 각 누적 경계를 한 프레임 이내로 검증하며, 개수와 전체 길이만 맞는 자료도 불규칙한 분할이면 거부한다.
- 원본 세션을 재분석하거나 결과를 복제하지 않는다. `run`은 기존 사용자 인증과 공개 upload/chunk/sensor/merge API만 이용한다. profile ID를 session ID에 넣지 않으며 GCS 경로는 `videos/10/{새 session ID}/`다.
- 일반 회귀 검사는 `cd api && go test ./cmd/live-replay -count=1`로 실행한다. Ginkgo/Gomega, 실제 센서 parser, transport로 대체한 HTTP/GCS와 로컬 영상 검사를 사용하며 운영의 유료 재생을 실행하지 않는다.

관련 계약: [인증](auth.md), [센서 timeline과 시간축](sensor-timeline.md), [backend 테스트](backend-testing.md).
