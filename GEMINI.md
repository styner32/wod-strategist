# WOD Strategist - Project Context & Rules

## 1. Project Identity

- **Name:** WOD Strategist
- **Goal:** AI-powered CrossFit coaching app providing real-time pose estimation, rep counting, and form correction.
- **Core Value:** "Smart Strategy" - Safe, data-driven workouts using On-device Vision AI.
- **User Persona:** Backend Engineer building a Mobile App (Focus on clean architecture & logic separation).

## 2. Tech Stack (Strict Constraints)

### Core Framework

- **Runtime:** React Native (0.80+)
- **Framework:** Expo SDK 54+ (**Prebuild/CNG Required**) - Do NOT suggest Expo Go.
- **Language:** TypeScript (Strict Mode)
- **Navigation:** Expo Router (File-based routing)

### AI & Vision (Native Modules)

- **Camera:** `react-native-vision-camera` (v4+)
- **Inference:** `react-native-fast-tflite` (with GPU Delegate)
- **Multithreading:** `react-native-worklets-core` (CRITICAL for Frame Processors)
- **Rendering:** `@shopify/react-native-skia` (High-performance 60fps overlays)

### Backend (Go API)

- **Language:** Go (1.24+)
- **Framework:** Gin (HTTP), GORM (Database), Asynq (Background Tasks)
- **Database:** PostgreSQL & Redis
- **AI Integration:** Google Generative AI (Gemini)

### State & Data

- **Global State:** Zustand
- **Local Cache:** React Query (Optional)

## 3. Architecture & Folder Structure

We follow a **Feature-Sliced** architecture. Keep logic separated from UI.

```text
/
├── api/                 # Go Backend (Gin, GORM, Asynq)
├── app/                 # Expo Router Pages
├── components/          # Shared UI Components
├── features/            # Domain Logic (The Core)
│   ├── ai-coach/        # Vision & AI Logic
│   │   ├── frame-processors/ # Worklet functions (Run on Vision Thread)
│   │   ├── inference/   # TFLite model loaders
│   │   └── mathematics/ # Pure Geometry/Vector logic (Unit testable, No React deps)
│   └── wod/             # WOD Management
├── store/               # Global Stores (Zustand)
└── assets/models/       # .tflite files
```
