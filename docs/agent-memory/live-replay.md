# Live 영상·Polar H10 재생 테스트

큰 업데이트 뒤 수동으로 실행하는 운영 API 통합 점검이다. 기존 녹화의 입력을 새 `WOD-YYYYMMDD-ULID` 세션으로 보내며, **대상 profile은 10으로 고정하고 결과를 보존한다.** 실제 업로드·작업 큐·AI 분석을 실행하므로 분석 비용이 발생한다. 자동 CI나 정기 실행에 연결하지 않는다. 카메라·BLE 하드웨어 자체의 검증은 별도다.

## 최초 준비

- Go, `ffmpeg`, `ffprobe`가 필요하다. `prepare`에는 원본 DB 읽기 권한, GCS 읽기용 Application Default Credentials와 DB 접속 경로가 필요하다. Cloud SQL Proxy 접속은 [README의 DB 안내](../../README.md#cloud-sql-dev)를 참고한다.
- 저장된 원본은 profile **1**의 `WOD-20260930-01M3QZH8TPXYMBGP9ER5W6SMBB`다. 기본 구간은 서버 `merged.mp4`의 **04:50 이상 09:50 미만**, 기본 조각 길이는 10초다.
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

`test-live-prepare`와 `test-live`는 Makefile의 기존 `.env` include/export를 생략한다. CLI가 `api/.env.live.local` → 루트 `.env` → `api/.env` 순으로 비어 있는 값을 채운다. **이미 설정된 프로세스 환경변수가 우선**하므로 이전에 export한 DB/API 주소가 있으면 먼저 확인한다. `run`에는 DB·GCS 관리자 권한이 필요 없다. 토큰이 있으면 `LIVE_TEST_JWT`를 우선 사용하며, 없으면 username/password로 로그인하고 profile 10 소유권을 확인한다.

저장소 루트에서 한 번 준비한다.

```bash
make -C api test-live-prepare
```

기본 출력은 `api/tmp/live-replay/fixture/`의 `manifest.json`, `sensor.ndjson`, `chunks/*.mp4`다. 영상·센서의 원본 object/generation/hash, 시간 매핑과 잘라낸 샘플 수를 manifest에 남긴다. 기존 fixture는 덮어쓰지 않으므로 다시 준비할 때는 새 위치를 지정한다.

```bash
make -C api test-live-prepare ARGS='--fixture=tmp/live-replay/fixture-v2'
```

이미 준비된 자료의 영상 분할만 다시 만들 때는 DB 없이 기존 manifest와 센서를 재사용한다. 원본 영상은 저장된 generation·SHA256으로 고정해 GCS에서 읽으며, 센서 파일은 그대로 보존한다. 새 자료를 검증한 뒤 기본 `fixture/`와 교체하며 이전 폴더는 별도 보관한다.

```bash
make -C api test-live-prepare ARGS='--from-fixture=tmp/live-replay/fixture --fixture=tmp/live-replay/fixture-v2'
```

## 반복 실행과 결과 확인

```bash
make -C api test-live
# 다른 fixture / 더 긴 분석 대기시간
make -C api test-live ARGS='--fixture=tmp/live-replay/fixture-v2 --analysis-timeout=45m'
# 전체 옵션과 기본값 확인
make -C api test-live-prepare ARGS='-h'
make -C api test-live ARGS='-h'
```

- 입력 재생은 약 5분이다. 각 조각의 capture 종료 시각까지 기다린 뒤 영상과 quality-filtered peak BPM을 전송한다. 마지막 조각 뒤 `calculation_version:3` 센서를 업로드하고, 완료된 timeline을 확인한 다음 merge를 요청한다. 이 순서는 센서 처리가 끝나기 전에 `activity:review`가 실행되는 경합을 막는다.
- 기본 polling 간격은 5초, 센서 완료 대기는 5분, merge 이후 분석 대기는 30분이다. `--poll-interval`, `--sensor-timeout`, `--analysis-timeout`, `--output-dir`로 조절한다.
- 결과는 `api/tmp/live-replay/runs/{새 session ID}/report.json`에 저장한다. 재식별한 `sensor.ndjson`과 확인용 `merged.mp4`도 같은 실행 폴더에 저장한다. 실패·중단 시 보고서와 이미 생성된 운영 세션을 보존한다. HTTP 요청을 자동 재전송하지 않으며, 명령을 다시 실행하면 새 세션을 만든다.
- `passed`는 업로드·분석 완료, 입력/결과의 session·profile·센서 version·샘플/시간 정합성, activity review, 병합 영상 길이 확인을 뜻한다. **동작 분류·반복 수·코칭 내용의 정확도는 보고서와 웹 화면에서 수동 검토한다.** 계정 권한, 서버 기능 설정, 분석 실패와 timeout은 해당 단계의 실패로 기록한다.
- 보고서의 `profile_before`, `profile_after`, 센서 `calculation_inputs`를 함께 확인한다. 보존된 테스트는 일반 profile 10 기록으로 취급된다. 유효 ACC가 120초 이상인 v3 완료 세션이 3개 이상 쌓이면 기존 worker가 개인 ENMO calibration을 자동 설정할 수 있으므로, 반복 결과가 이전과 항상 동일하다고 가정하지 않는다.

## 구현 시 지킬 계약

- 영상 시간과 센서 capture 시간을 혼용하지 않는다. 실제 추출한 기본 구간은 media 300초에 capture 약 **299.841697초**, HR **300개**, ACC **15,360개**다. DB의 전체 정밀도를 사용한 정확한 경계는 manifest로 확인한다. capture/media 매핑과 원래 ACC `dt`·sampling rate를 보존하고, 관측되지 않은 샘플을 보간하지 않는다.
- NDJSON의 ACC 배열은 샘플 단위로 자른다. 활성 device/stream 문맥을 0초에 복원하고 pause/gap·footer·identity·byte size/SHA256을 새 구간에 맞춘다. device nanosecond timestamp와 모르는 JSON 필드는 정확히 보존한다. 영상 조각은 오디오를 제외한다. AAC encoder padding이 worker가 사용하는 컨테이너 `format.duration`을 늘려 누적 시간 매핑을 어긋나게 하기 때문이다.
- 영상은 `setpts=PTS-STARTPTS`로 시작 시각을 맞추고 `segment_time_delta=1/(2*FPS)`를 적용한다. 분할 지점보다 미세하게 빠른 키프레임을 놓쳐 첫 조각이 18.333초가 되던 문제를 막는다. manifest의 `chunk_seconds`(이전 자료의 기본값 10)와 각 누적 경계를 한 프레임 이내로 검증하며, 개수와 전체 길이만 맞는 자료도 불규칙한 분할이면 거부한다.
- 원본 세션을 재분석하거나 결과를 복제하지 않는다. `run`은 기존 사용자 인증과 공개 upload/chunk/sensor/merge API만 이용한다. profile ID를 session ID에 넣지 않으며 GCS 경로는 `videos/10/{새 session ID}/`다.
- 일반 회귀 검사는 `cd api && go test ./cmd/live-replay -count=1`로 실행한다. Ginkgo/Gomega, 실제 센서 parser, transport로 대체한 HTTP/GCS와 로컬 영상 검사를 사용하며 운영의 유료 재생을 실행하지 않는다.

관련 계약: [인증](auth.md), [센서 timeline과 시간축](sensor-timeline.md), [backend 테스트](backend-testing.md).
