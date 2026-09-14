# 2026-09-09 H10 저측정 및 마지막 partial 병합 실패

기준 코드: `d7ea80d`. 사용자는 이전 변경을 merge·배포했다고 보고했다. 이번 조사는 업로드된 두 센서 원본과 iPhone 앱의 임시 영상 파일을 읽어 수행했다. 설치된 앱의 정확한 commit과 운영 DB의 저장 요약은 직접 확인하지 않았다.

## 심박: 원본부터 낮고, 현재 필터가 통과시킨 사례

| 항목 | 첫 번째 WARMUP | 두 번째 WOD |
|---|---:|---:|
| 원본 HR 이벤트 수 | 1,509 | 559 |
| 원본 최소–최대 BPM | 80–133 | 60–129 |
| 시간 가중 평균 BPM | 101.27 | 92.49 |
| 집계 대상 시간 | 1,511.178초 | 925.596초 |
| 유효 / 제외 / 미측정 시간 | 1,507.965 / 0 / 3.213초 | 558.698 / 0 / 366.898초 |
| 유효 비율 | 99.79% | 60.36% |
| `contact` 정보 | 모든 이벤트에서 없음 | 모든 이벤트에서 없음 |

평균·시간·비율은 현재 TypeScript/Go의 계산 버전 2로 원본을 로컬 재생한 결과다. 운영 저장 요약을 조회하거나 재계산한 것은 아니다. 최대심박 기준을 제공하지 않은 재생이므로 존 분포는 검증하지 않았다.

- 세션: `WARMUP-20260909-01M21RXYEEAH6CH105456CECRN`, `WOD-20260909-01M21X9A35GGJA27HC79CHZ2E3`.
- 두 번째는 10:42:27.174 KST에 시작했다. 원본에 160 BPM 이상은 없고, 344.393–404.393초 구간의 80–90 BPM 60개 이벤트가 모두 통과한다. 이후에도 완만한 하강과 긴 동일값 구간이 있다.
- 현재 필터는 급락·명시적 접촉 불량·미수신을 감지한다. 이번의 점진적인 저측정에는 급락 기준이 성립하지 않았다. 유효 비율 60.36%는 정확도나 생리학적 신뢰도를 뜻하지 않는다.
- 마지막 HR은 561.418초, 연결 공백 시작 이벤트는 562.112초다. 나머지 시간은 미측정으로 남는다. 연결 공백 시각을 스트랩을 뗀 정확한 시각으로 해석하지 않는다.
- RR도 일정한 절반 심박 패턴이 아니다. 150초 이후 412개 알림 중 172개에 RR이 없고, 보고된 RR 376개 중 50개가 2초를 넘는다. RR 없이 BPM만 들어오는 것은 프로토콜상 가능하므로, 이 비율만으로 새 자동 제외 기준을 확정할 수 없다. [Bluetooth Heart Rate Service 3.1](https://www.bluetooth.com/wp-content/uploads/Files/Specification/HTML/HRS_v1.0/out/en/index-en.html)
- 접촉 필드가 없다는 사실만으로 H10이 접촉 플래그를 지원하지 않는다고 단정할 수 없다. 수신 flags 원본과 앱 build 정보가 없어 미지원과 기록 경로를 구분하지 못했다.
- 사용자 설명과 원본은 측정 이상을 의심할 충분한 근거지만, Apple Watch의 동시 원본이 없으므로 실제 BPM이나 정확한 고장 원인을 복원하지 않았다. 임의의 2배 보정·저심박 일괄 제외·오늘 기록 덮어쓰기는 수행하지 않았다.

**판정:** 버전 2 자동 테스트 통과와 별개로, 이번 H10 실기기 품질 검증은 통과하지 못했다. RR·원시 flags를 포함한 비교 녹화로 지속 저측정 탐지 기준을 검증하고 별도 계산 버전으로 도입할 필요가 있다.

## 영상: 실제 partial로 네이티브 실패 재현

사용자가 확정한 원본 경로: `api/tmp/20260909_failed/7313D478-397F-484B-900B-2B975F33D1B4.mov`. 초기 조사에서 iPhone 앱의 `tmp/` 루트에서 복사한 파일과 크기(973,984바이트) 및 SHA-256이 일치하므로, 앞선 병합 재현은 동일한 파일에 대한 결과다. 원본은 삭제·수정하지 않았다.

SHA-256: `c4dff39072ace5ffb2bea70be78fa5c315e30f58e8962952496ffb7980cc4a2f`.

- 파일 크기 973,984바이트, HEVC 2프레임, ffprobe 기준 0.066667초, 오디오 없음. ffmpeg 전체 디코딩은 오류 없이 끝난다.
- AVFoundation은 asset 및 video track 길이를 모두 **0초**로 읽는다. 따라서 파일 존재·0바이트 검사만으로 막을 수 없다.
- 직전 정상 청크 `75FA4457-504C-4E50-92D6-11455DE565B0.mov`와 함께, 저장소의 실제 Swift 병합 구현을 추출한 macOS 실행기로 비교했다.

| 입력 / 구현 | 결과 |
|---|---|
| 정상 청크 / HEAD | 성공 |
| partial 단독 / HEAD | 실패: AVFoundation -11800, 내부 OSStatus -12780 |
| 정상 청크 + partial / HEAD | 동일 실패 |
| 정상 청크 + partial / 작업 트리 | 0초 partial 제외 후 성공, 출력 디코딩 오류 없음 |

작업 시작 전에 이미 있던 미커밋 수정은 모바일의 0.2초 미만 청크 제외, JS의 0바이트 제외, iOS 네이티브의 무효 길이/트랙 처리, Android의 빈 청크 타임스탬프 처리였다. 이 중 iOS의 0초 청크 제외가 실제 재현 파일의 실패를 막는 것을 확인했다.

이번에 별도로 수정한 문제는 마지막 파일 저장 완료를 기다리는 순서다. VisionCamera의 `stopRecording()`은 파일 쓰기 완료 전에 반환한다. 타이머가 stop을 요청한 직후 사용자가 종료하면, 기존 코드는 마지막 콜백을 기다리지 않고 병합하거나 마지막 업로드를 건너뛸 수 있었다. 청크별 완료 상태를 두고 타이머·사용자 종료가 같은 콜백을 기다리도록 변경했다. 파일 경로와 업로드 수를 등록한 후 대기를 해제한다.

## 함께 발견한 센서 파일 형식 불일치

두 원본의 `stream_start.clock_anchor.device_timestamp_ns`는 정밀도 보존을 위한 십진 문자열인데 Go는 `int64` JSON 숫자만 받아, 스트림 시작 이벤트 전체의 파싱을 건너뛰고 있었다. 해당 필드가 기존 숫자와 문자열을 모두 정밀도 손실 없이 받도록 수정했다. HR 품질 기준과 계산 버전은 변경하지 않았다.

nanosecond anchor 자체는 이후 집계에 사용되지 않는다. 다만 같은 이벤트의 `sampling.acc_hz`도 함께 유실되어 움직임 집계가 기본 50Hz를 쓰는 부수 문제가 있었다. 25Hz 스트림에서 실제로 유효 창을 미측정으로 분류하는 재현 테스트를 추가했다. 이 형식 불일치가 이번 낮은 BPM의 원인이라는 근거는 없다.

이번 두 원본은 모두 50Hz 설정이어서 수정 전후 HR·ACC 전체 Metrics가 일치했다. 두 파일을 계산 버전 1·2로 각각 재생한 네 결과에서 형식 경고가 사라졌다. 결과는 `/tmp/wod-sep09-evidence/go-replay-fixed.json`에 보관했다.

## 이번 변경 파일

| 파일 | 변경 |
|---|---|
| `app/workout/visionTestPage.tsx` | 마지막 파일 콜백 대기를 보강; 기존 micro-chunk 변경 유지 |
| `features/wod/chunkRecordingCompletion.ts` | 청크별 stop·완료 조정 |
| `features/wod/__tests__/chunkRecordingCompletion.test.ts` | 종료 순서 회귀 테스트 5개 |
| `api/internal/sensor/types.go`, `clock_anchor.go` | 문자열·숫자 nanosecond timestamp 호환 파싱 |
| `api/internal/sensor/clock_anchor_test.go` | 정밀도·형식 오류·ACC sampling rate 회귀 테스트 |
| `docs/agent-memory/chunk-finalization.md`, `heart-rate-quality.md` | 구현 제약 및 실패한 H10 실기기 검증 기록 |

작업 시작 전에 있던 `features/wod/mergeChunksLocal.ts`, 그 테스트 및 iOS/Android `VideoMergerModule` 수정도 그대로 보존했다.

## 검증 및 증거 위치

- 로컬 증거: `/tmp/wod-sep09-evidence/`. 원본 NDJSON·MOV, TypeScript/Go 재생 결과, Swift 실행기 및 비교 로그를 보관했다. 원본 생체 데이터와 영상은 저장소에 추가하지 않았다.
- 새 회귀 테스트: `features/wod/__tests__/chunkRecordingCompletion.test.ts`. 타이머 종료 후 늦은 콜백, 사용자 종료와 중복 stop 방지, 오류/폐기, 5초 제한을 확인한다.
- 명령: `npm test -- --runInBand features/health features/wod` → 15개 스위트 / 148개 테스트 통과. `npm run typecheck` → 통과. `git diff --check` → 통과.
- 백엔드 명령 (`api/`에서): `go test -p 1 ./internal/sensor` → 통과. timestamp 호환 관련 Ginkgo 사례 12개 추가.
- 영상 검사: `ffprobe`, `ffmpeg -v error -i <merged-output> -f null -`; 실제 Swift 함수를 `swiftc`로 컴파일한 HEAD/작업 트리 실행기에 동일 입력을 전달했다.
- 클라우드 원본 조회에 사용한 정확한 명령:

```sh
gcloud storage ls 'gs://wod-strategist-uploads-dev/videos/1/*20260909*/sensor_telemetry*.ndjson'
gcloud storage cp 'gs://wod-strategist-uploads-dev/videos/1/*20260909*/sensor_telemetry*.ndjson' /tmp/wod-sep09-evidence/
```

네이티브 수정은 앱 재빌드가 필요하다. 이번 macOS AVFoundation 재현은 수정된 iPhone 앱에서의 촬영·종료·갤러리 저장 검증을 대체하지 않는다. 마지막 콜백 대기의 기존 5초 제한도 남아 있다. 이번 조사에서 commit·push·배포·기존 세션 변경은 수행하지 않았다.
