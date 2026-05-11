import { useState, useRef, type FormEvent, type ChangeEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import { uploadApi, uploadToGCS, validateFileSize } from '../api/upload';

function generateSessionId() {
  const now = new Date();
  const date = now.toISOString().slice(0, 10).replace(/-/g, '');
  const rand = crypto.randomUUID().replace(/-/g, '').slice(0, 12);
  return `WOD-${date}-${rand}`;
}

export function UploadPage() {
  const { user } = useAuth();
  const profiles = user?.profiles || [];
  const navigate = useNavigate();

  const [selectedProfileId, setSelectedProfileId] = useState<number>(
    profiles.length > 0 ? profiles[0].id : 0,
  );
  const [file, setFile] = useState<File | null>(null);
  const [workoutType, setWorkoutType] = useState('wod');
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [error, setError] = useState('');
  const [phase, setPhase] = useState<'idle' | 'uploading' | 'completing'>('idle');
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleFileChange = (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (!f) return;

    const validationError = validateFileSize(f);
    if (validationError) {
      setError(validationError);
      setFile(null);
      return;
    }

    setError('');
    setFile(f);
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!file || !selectedProfileId) return;

    setError('');
    setUploading(true);
    setPhase('uploading');
    setProgress(0);

    try {
      const sessionId = generateSessionId();

      // 1. Get signed upload URL
      const { upload_url, gcs_uri } = await uploadApi.getUploadUrl(
        sessionId,
        file.name,
        selectedProfileId,
      );

      // 2. Upload file to GCS
      await uploadToGCS(upload_url, file, setProgress);

      // 3. Complete upload and enqueue analysis
      setPhase('completing');
      await uploadApi.completeUpload({
        session_id: sessionId,
        gcs_uri,
        profile_id: selectedProfileId,
        workout_type: workoutType,
        movements: [],
        injuries: [],
      });

      // 4. Navigate to session detail
      navigate(`/sessions/${sessionId}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Upload failed');
      setPhase('idle');
    } finally {
      setUploading(false);
    }
  };

  const formatFileSize = (bytes: number) => {
    if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
    if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(0)} MB`;
    return `${(bytes / 1024).toFixed(0)} KB`;
  };

  return (
    <div className="max-w-2xl mx-auto">
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-text-primary">Upload Workout Video</h1>
        <p className="text-text-secondary mt-1">
          Upload a recorded workout video for AI analysis. Maximum file size: 2 GB.
        </p>
      </div>

      <form
        onSubmit={handleSubmit}
        className="bg-bg-elevated border border-border rounded-xl p-6 space-y-6"
      >
        {error && (
          <div className="bg-error/10 border border-error/30 text-error text-sm rounded-lg px-4 py-3">
            {error}
          </div>
        )}

        {/* File picker */}
        <div>
          <label className="block text-sm font-medium text-text-secondary mb-2">
            Video File
          </label>
          <div
            onClick={() => fileInputRef.current?.click()}
            className="border-2 border-dashed border-border hover:border-accent/50 rounded-xl p-8 text-center cursor-pointer transition-colors group"
          >
            <input
              ref={fileInputRef}
              type="file"
              accept="video/*"
              onChange={handleFileChange}
              className="hidden"
            />
            {file ? (
              <div className="flex items-center justify-center gap-3">
                <div className="w-10 h-10 rounded-lg bg-accent/10 flex items-center justify-center">
                  <svg className="w-5 h-5 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z" />
                  </svg>
                </div>
                <div className="text-left">
                  <p className="text-sm font-medium text-text-primary">{file.name}</p>
                  <p className="text-xs text-text-muted">{formatFileSize(file.size)}</p>
                </div>
              </div>
            ) : (
              <>
                <svg className="w-10 h-10 text-text-muted mx-auto mb-3 group-hover:text-accent transition-colors" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5m-13.5-9L12 3m0 0 4.5 4.5M12 3v13.5" />
                </svg>
                <p className="text-sm text-text-secondary">
                  Click to select a video file
                </p>
                <p className="text-xs text-text-muted mt-1">
                  MP4, MOV, or WebM up to 2 GB
                </p>
              </>
            )}
          </div>
        </div>

        {/* Profile */}
        {profiles.length > 1 && (
          <div>
            <label htmlFor="upload-profile" className="block text-sm font-medium text-text-secondary mb-1.5">
              Profile
            </label>
            <select
              id="upload-profile"
              value={selectedProfileId}
              onChange={(e) => setSelectedProfileId(Number(e.target.value))}
              className="w-full bg-bg-secondary border border-border rounded-lg px-3 py-2.5 text-text-primary focus:outline-none focus:ring-2 focus:ring-accent/50"
            >
              {profiles.map((p) => (
                <option key={p.id} value={p.id}>{p.name}</option>
              ))}
            </select>
          </div>
        )}

        {/* Workout Type */}
        <div>
          <label htmlFor="upload-workout-type" className="block text-sm font-medium text-text-secondary mb-1.5">
            Workout Type
          </label>
          <select
            id="upload-workout-type"
            value={workoutType}
            onChange={(e) => setWorkoutType(e.target.value)}
            className="w-full bg-bg-secondary border border-border rounded-lg px-3 py-2.5 text-text-primary focus:outline-none focus:ring-2 focus:ring-accent/50"
          >
            <option value="wod">WOD (Workout of the Day)</option>
            <option value="crossfit">CrossFit</option>
            <option value="weight_training">Weight Training</option>
            <option value="surfing">Surfing</option>
            <option value="yoga">Yoga</option>
          </select>
        </div>

        {/* Progress bar */}
        {uploading && (
          <div>
            <div className="flex items-center justify-between text-sm mb-2">
              <span className="text-text-secondary">
                {phase === 'uploading' ? 'Uploading...' : 'Starting analysis...'}
              </span>
              <span className="text-text-primary font-mono">{progress}%</span>
            </div>
            <div className="h-2 bg-bg-secondary rounded-full overflow-hidden">
              <div
                className="h-full bg-gradient-to-r from-accent to-purple-400 rounded-full transition-all duration-300 ease-out"
                style={{ width: `${progress}%` }}
              />
            </div>
          </div>
        )}

        <button
          type="submit"
          disabled={!file || uploading || !selectedProfileId}
          className="w-full bg-accent hover:bg-accent-hover text-white font-medium rounded-lg px-4 py-3 transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
        >
          {uploading ? 'Uploading...' : 'Upload & Analyze'}
        </button>
      </form>
    </div>
  );
}
