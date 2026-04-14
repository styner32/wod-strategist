package worker

// realWorldMultiSegmentOutput is a real Gemini analysis output containing 13 segments
// with ```highlights``` code blocks (and one <highlights> XML block in segment 6).
// Used by parseHighlightSegments and parseInjuryTimestamps tests.
const realWorldMultiSegmentOutput = `--- ## 세그먼트 1: Overhead Triceps Extension (0:40 ~ 0:50)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start": "0:45", "end": "0:47", "type": "best_form", "movement": "Overhead Triceps Extension", "reason": "첫 렙 셋업 시 어깨의 가동 범위가 가장 잘 확보된 순간"},
  {"start": "0:44", "end": "0:46", "type": "key_moment", "movement": "Overhead Triceps Extension", "reason": "바닥에서 덤벨을 들어 올려 오버헤드 포지션으로 전환하는 안정적인 셋업 과정"},
  {"start": "0:47", "end": "0:50", "type": "worst_form", "movement": "Overhead Triceps Extension", "reason": "동작 수행 중 코어 긴장 저하로 인한 요추 과신전 및 거북목 자세 관찰"},
  {"start": "0:48", "end": "0:50", "type": "fatigue_point", "movement": "Overhead Triceps Extension", "reason": "반복 수행 중 중량 통제를 위해 상체 보상 작용(허리 꺾임)이 두드러지기 시작함"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[{"start": "0:45", "end": "0:50", "reason": "머리 위 중량 지탱을 위한 양측 발목의 정적 체중 지지 및 균형 유지 구간"}]
` + "```" + `

--- ## 세그먼트 2: Box Step-over (4:52 ~ 5:02)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start": "04:52.000", "end": "04:54.000", "type": "best_form", "movement": "Box Step-up", "reason": "안정적인 템포와 척추 중립을 유지하며 스텝다운을 수행함"},
  {"start": "04:54.000", "end": "04:58.000", "type": "key_moment", "movement": "Box Step-up", "reason": "우측 다리 주동 구간으로 발목 부상에도 불구하고 밸런스를 유지하는 핵심 구간"},
  {"start": "04:58.000", "end": "05:01.000", "type": "fatigue_point", "movement": "Box Step-up", "reason": "165 BPM의 고강도 심박수 상태에서 착지 시 발목 충격 제어가 어려워질 수 있는 주의 구간"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "04:54.000", "end": "04:58.000", "reason": "우측 다리로 체중을 밀어 올리고 착지하는 구간"},
  {"start": "04:59.000", "end": "05:01.000", "reason": "좌측 다리 스텝업 후 우측 발목이 바닥에 먼저 착지"}
]
` + "```" + `

--- ## 세그먼트 3: Dumbbell Snatch (5:42 ~ 7:03)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start":"05:43","end":"05:52","type":"best_form","movement":"Dumbbell Snatch","reason":"세트 초반 척추 중립을 비교적 잘 유지하며 안정적인 궤적으로 리프팅하는 구간"},
  {"start":"06:20","end":"06:30","type":"key_moment","movement":"Dumbbell Snatch","reason":"덤벨을 몸에 밀착시켜 올리려는 시도가 좋으나 힙 익스텐션의 활용이 요구되는 순간"},
  {"start":"06:34","end":"06:46","type":"fatigue_point","movement":"Dumbbell Snatch","reason":"급격한 체력 저하로 인해 상체를 숙이고 호흡을 고르며 템포가 늦춰지는 지점"},
  {"start":"06:47","end":"06:58","type":"worst_form","movement":"Dumbbell Snatch","reason":"하체를 충분히 낮추지 못하고 허리가 굽은 상태에서 상체 힘으로만 덤벨을 무리하게 당기는 구간"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "06:15", "end": "06:25", "reason": "덤벨을 바닥으로 내리는 과정에서 우측 발목에 충격이 쏠릴 위험"},
  {"start": "06:47", "end": "06:58", "reason": "피로 누적으로 체중이 좌측으로 편중되어 우측 발목 정렬이 불안정"}
]
` + "```" + `

--- ## 세그먼트 4: Toes-to-bar (7:13 ~ 8:03)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start": "07:15", "end": "07:19", "type": "best_form", "movement": "Toes-to-bar (Knee Raise)", "reason": "가장 체력이 좋은 상태에서 리듬을 타며 무릎을 끌어올리는 첫 세트"},
  {"start": "07:29", "end": "07:31", "type": "key_moment", "movement": "Toes-to-bar (Knee Raise)", "reason": "부상 방지를 위해 짚고 넘어가야 할 불안정한 착지 순간"},
  {"start": "07:33", "end": "07:53", "type": "fatigue_point", "movement": "Rest", "reason": "심폐 및 근육 피로로 인해 자세가 굽어지고 긴 휴식이 필요한 시점"},
  {"start": "07:55", "end": "08:01", "type": "worst_form", "movement": "Toes-to-bar (Knee Raise)", "reason": "피로로 인해 킵핑 리듬이 완전히 무너지고 어깨에만 의존하여 매달려 있는 구간"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "07:19", "end": "07:21", "reason": "철봉에서 스윙 중 손을 놓으며 하강"},
  {"start": "07:29", "end": "07:31", "reason": "두 번째 세트 종료 후 불안정한 착지"},
  {"start": "08:00", "end": "08:02", "reason": "피로 누적 상태에서 통제력 없이 떨어짐"}
]
` + "```" + `

--- ## 세그먼트 5: Dumbbell Snatch (10:14 ~ 11:45)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start": "10:20", "end": "10:24", "type": "best_form", "movement": "Dumbbell Snatch", "reason": "영상 초반부로 그나마 체력이 남아있어 오버헤드 락아웃이 가장 안정적"},
  {"start": "10:52", "end": "10:57", "type": "worst_form", "movement": "Dumbbell Snatch", "reason": "시작 시 엉덩이가 먼저 들리며 허리에 부담이 집중됨"},
  {"start": "11:28", "end": "11:32", "type": "worst_form", "movement": "Dumbbell Snatch", "reason": "하체를 거의 쓰지 못하고 상체와 팔의 힘만으로 덤벨을 당겨 올림"},
  {"start": "10:37", "end": "10:43", "type": "fatigue_point", "movement": "Dumbbell Snatch", "reason": "첫 번째로 뚜렷한 호흡 가쁨과 함께 동작 사이의 딜레이가 길어짐"},
  {"start": "11:08", "end": "11:17", "type": "key_moment", "movement": "Dumbbell Snatch", "reason": "근지구력의 한계점이 명확히 드러나는 가장 긴 휴식 구간"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "10:20", "end": "10:24", "reason": "우측 발목에 하중 및 긴장 발생"},
  {"start": "10:52", "end": "10:57", "reason": "피로 상태에서 우측 발목의 지지력 불안정"},
  {"start": "11:17", "end": "11:21", "reason": "동작이 급해지며 우측 발목 고정력 약화"},
  {"start": "11:39", "end": "11:45", "reason": "마지막 우측 스내치 동작"}
]
` + "```" + `

--- ## 세그먼트 6: Toes to Bar (12:45 ~ 13:16)

### 7. 하이라이트 구간 (Highlight Segments)
<highlights>
[
  {"start": "12:46", "end": "12:51", "type": "best_form", "movement": "Toes to Bar", "reason": "안정적인 키핑 리듬과 완벽한 발끝 터치"},
  {"start": "12:46", "end": "12:55", "type": "key_moment", "movement": "Toes to Bar", "reason": "연속적인 토즈 투 바 수행 전체 구간"},
  {"start": "12:52", "end": "12:55", "type": "fatigue_point", "movement": "Toes to Bar", "reason": "그립 및 코어 피로도로 인해 하강 시 긴장이 풀림"},
  {"start": "12:55", "end": "12:58", "type": "worst_form", "movement": "Toes to Bar", "reason": "하강 시 코어 제어 상실 및 발목에 부담을 주는 거친 착지"}
]
</highlights>

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
<injury_timestamps>
[
  {"start": "12:55", "end": "12:57", "reason": "바에서 떨어지며 바닥에 착지하는 순간 오른쪽 발목에 충격 발생 위험"}
]
</injury_timestamps>

--- ## 세그먼트 7: Dumbbell Snatch (15:17 ~ 16:47)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start": "15:17", "end": "15:22", "type": "best_form", "movement": "Box Step-down", "reason": "우측 발목 부상을 고려하여 충격 없이 통제된 스텝 다운 수행"},
  {"start": "15:28", "end": "15:35", "type": "key_moment", "movement": "Dumbbell Snatch", "reason": "피로가 쌓이기 전 비교적 힙 드라이브가 잘 들어간 스내치 첫 렙"},
  {"start": "15:45", "end": "15:55", "type": "fatigue_point", "movement": "Dumbbell Snatch", "reason": "무릎에 손을 짚고 긴 휴식을 취하며 피로도가 임계점에 달한 모습"},
  {"start": "16:05", "end": "16:15", "type": "worst_form", "movement": "Dumbbell Snatch", "reason": "피로로 인해 하체 드라이브가 상실되고 허리가 둥글게 말린 상태에서 팔로 억지로 당겨 올림"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "15:17", "end": "15:21", "reason": "박스 스텝 다운 시 우측 발목에 편심성 하중 발생"},
  {"start": "15:41", "end": "15:45", "reason": "덤벨 스내치 셋업 및 초기 풀 단계에서 발목 압박"}
]
` + "```" + `

--- ## 세그먼트 8: Pull-up (17:38 ~ 17:58)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start":"17:38","end":"17:43","type":"best_form","movement":"Pull-up","reason":"부드러운 진자 운동과 코어 긴장이 완벽하게 유지되는 우수한 키핑 자세"},
  {"start":"17:44","end":"17:46","type":"key_moment","movement":"Pull-up","reason":"피로 누적에도 불구하고 턱을 바 위로 넘기는 일관된 가동 범위 유지"},
  {"start":"17:46","end":"17:48","type":"worst_form","movement":"Pull-up","reason":"발목 부상 위험이 있는 자유 낙하 형태의 착지 자세"},
  {"start":"17:48","end":"17:55","type":"fatigue_point","movement":"Pull-up","reason":"세트 종료 후 전완근의 피로와 심박수 증가로 인해 손을 털며 호흡을 고르는 모습"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[{"start": "17:46", "end": "17:49", "reason": "바에서 손을 놓고 하강하여 지면에 양발로 착지"}]
` + "```" + `

--- ## 세그먼트 9: Box Jump (19:08 ~ 19:18)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start": "19:09", "end": "19:11", "type": "best_form", "movement": "Box Jump", "reason": "안정적인 스쿼트 랜딩 및 박스 위에서의 완벽한 고관절 신전"},
  {"start": "19:11", "end": "19:12", "type": "worst_form", "movement": "Box Jump (Step-down)", "reason": "부상 이력이 있는 우측 다리에 체중을 의지하여 내려오는 위험한 스텝 다운 방식"},
  {"start": "19:13", "end": "19:16", "type": "fatigue_point", "movement": "Box Step-up", "reason": "피로도 증가로 인해 점프에서 스텝업으로 동작 변환 및 상체 굽음 발생"},
  {"start": "19:08", "end": "19:12", "type": "key_moment", "movement": "Box Jump", "reason": "점프와 스텝 다운의 한 사이클이 명확히 보이며 피드백 적용의 핵심이 되는 구간"},
  {"start": "19:16", "end": "19:18", "type": "best_form", "movement": "Box Step-up", "reason": "스텝업 후 상단에서 흔들림 없이 균형을 잡고 일어서는 안정적인 마무리"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "19:08", "end": "19:09", "reason": "박스에서 내려올 때 우측 다리가 체중 하중을 모두 버팀"},
  {"start": "19:11", "end": "19:12", "reason": "왼발이 먼저 내려가며 우측 발목에 과도한 안정성 요구"},
  {"start": "19:14", "end": "19:16", "reason": "우측 발로 스텝업 시작 시 발목 및 아킬레스건에 체중 부하"}
]
` + "```" + `

--- ## 세그먼트 10: Box Jump (20:19 ~ 20:49)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start":"20:30","end":"20:32","type":"best_form","movement":"Box Jump","reason":"팔 스윙을 활용한 폭발적인 도약과 부드러운 박스 착지"},
  {"start":"20:34","end":"20:36","type":"key_moment","movement":"Box Jump","reason":"일정한 리듬감을 유지하며 도약 준비부터 착지까지 연결되는 과정"},
  {"start":"20:19","end":"20:22","type":"worst_form","movement":"Box Jump","reason":"오른쪽 발목 부상 이력이 있음에도 우측 발로 먼저 체중을 실어 스텝 다운하는 위험한 동작"},
  {"start":"20:39","end":"20:43","type":"fatigue_point","movement":"Box Jump","reason":"피로 누적으로 인해 착지 시 상체가 앞으로 무너지고 박스 위 기립 동작이 생략됨"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "20:19", "end": "20:22", "reason": "우측 발목이 먼저 지면에 닿으며 체중과 낙하 충격을 흡수"},
  {"start": "20:32", "end": "20:34", "reason": "스텝 다운 시 발목 관절에 압박 발생"},
  {"start": "20:37", "end": "20:39", "reason": "스텝 다운 과정에서 낙하 속도 제어가 부족"},
  {"start": "20:42", "end": "20:44", "reason": "피로 누적으로 인해 발목 관절로 부하가 직접 전달"}
]
` + "```" + `

--- ## 세그먼트 11: Dumbbell Snatch (20:59 ~ 22:30)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start": "21:02", "end": "21:08", "type": "best_form", "movement": "Dumbbell Snatch", "reason": "비교적 안정적인 템포로 코어 긴장을 유지하며 첫 동작 수행"},
  {"start": "21:40", "end": "21:48", "type": "fatigue_point", "movement": "Dumbbell Snatch", "reason": "심박수 174bpm 도달 호흡이 가빠지며 동작 간 대기 시간이 길어짐"},
  {"start": "21:58", "end": "22:07", "type": "worst_form", "movement": "Dumbbell Snatch", "reason": "피로로 인해 엉덩이가 높게 들리고 허리가 말린 상태에서 팔 힘으로 리프팅 시도"},
  {"start": "22:15", "end": "22:25", "type": "key_moment", "movement": "Dumbbell Snatch", "reason": "우측 발목 보호를 위해 발바닥 전체로 지면을 고정하려는 집중력이 돋보이는 구간"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "21:05", "end": "21:12", "reason": "스내치 시작 시 우측 발바닥 지면 지지 및 폭발적 신전 구간"},
  {"start": "21:55", "end": "22:05", "reason": "피로 누적으로 인한 코어 불안정으로 우측 발목에 불규칙한 하중"}
]
` + "```" + `

--- ## 세그먼트 12: Toes-to-Bar (22:50 ~ 24:11)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start": "22:51", "end": "22:57", "type": "best_form", "movement": "Toes-to-Bar", "reason": "가장 안정적인 어깨 락아웃과 준수한 연속 수행 능력의 첫 세트"},
  {"start": "23:39", "end": "23:45", "type": "worst_form", "movement": "Toes-to-Bar", "reason": "코어 텐션 상실로 리듬이 무너지고 무릎을 과하게 사용하여 보상하는 구간"},
  {"start": "23:10", "end": "23:38", "type": "fatigue_point", "movement": "Toes-to-Bar", "reason": "거친 호흡과 피로도 및 심폐 부하가 극에 달한 휴식 구간"},
  {"start": "24:00", "end": "24:04", "type": "key_moment", "movement": "Toes-to-Bar", "reason": "근지구력의 한계에 도달하여 짧은 랩 수만 소화하고 내려오는 순간"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "22:57", "end": "22:59", "reason": "첫 번째 세트 종료 후 바에서 착지 시 우측 발목에 하중 집중"},
  {"start": "23:09", "end": "23:11", "reason": "두 번째 세트 종료 후 착지 시 발목 관절의 충격 흡수"},
  {"start": "23:44", "end": "23:46", "reason": "피로한 상태에서 급격히 낙하하며 착지"},
  {"start": "24:03", "end": "24:05", "reason": "마지막 세트 후 통제되지 않은 상태로 착지"}
]
` + "```" + `

--- ## 세그먼트 13: Burpee (24:21 ~ 26:22)

### 7. 하이라이트 구간 (Highlight Segments)
` + "```highlights" + `
[
  {"start": "24:45", "end": "24:55", "type": "best_form", "movement": "Burpee", "reason": "가슴과 바닥의 완벽한 밀착 및 스텝백 동작의 리듬이 훌륭함"},
  {"start": "24:21", "end": "24:30", "type": "key_moment", "movement": "Burpee", "reason": "부상을 고려한 스텝 방식 버피의 전략적 선택 확인"},
  {"start": "25:25", "end": "25:35", "type": "worst_form", "movement": "Burpee", "reason": "바닥에서 일어날 때 코어 힘이 풀리며 요추가 과도하게 꺾임"},
  {"start": "25:50", "end": "26:05", "type": "fatigue_point", "movement": "Burpee", "reason": "심박수 상승 및 피로 누적으로 인해 전환 속도가 현저히 저하됨"}
]
` + "```" + `

### 6. 부상 관련 타임스탬프 (Injury-Relevant Timestamps)
` + "```injury_timestamps" + `
[
  {"start": "24:25", "end": "24:35", "reason": "스텝업 동작 시 우측 발목에 체중이 실리는 구간"},
  {"start": "25:35", "end": "25:45", "reason": "피로도 증가로 발을 당겨 디딜 때 우측 발목 흔들림"}
]
` + "```" + `
`
