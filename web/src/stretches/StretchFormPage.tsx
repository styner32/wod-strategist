import { useState, useEffect, useMemo } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  stretchesApi,
  normalizeStretchKey,
  type Stretch,
  type StretchInput,
} from '../api/stretches';
import { uploadToGCS } from '../api/upload';

const CATEGORY_OPTIONS = [
  'Hips & Glutes',
  'Legs & Ankles',
  'Chest & Shoulders',
  'Spine & Back',
  'Arms & Neck',
];

const MAX_IMAGE_SIZE = 10 * 1024 * 1024; // 10 MB
const MAX_VIDEO_SIZE = 200 * 1024 * 1024; // 200 MB

export function StretchFormPage() {
  const { id } = useParams<{ id?: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const isEditMode = Boolean(id && id !== 'new');
  const targetId = isEditMode ? Number(id) : null;

  const { data: catalog = [], isLoading: isLoadingCatalog } = useQuery({
    queryKey: ['stretches'],
    queryFn: stretchesApi.list,
    staleTime: 10 * 60 * 1000,
  });

  const editingStretch = useMemo(() => {
    if (!isEditMode || !targetId) return null;
    return catalog.find((s) => s.id === targetId) || null;
  }, [isEditMode, targetId, catalog]);

  // Form State
  const [name, setName] = useState('');
  const [targetArea, setTargetArea] = useState('Hips & Glutes');
  const [description, setDescription] = useState('');
  const [durationHint, setDurationHint] = useState('');
  const [caution, setCaution] = useState('');
  const [aliases, setAliases] = useState<string[]>([]);
  const [aliasInput, setAliasInput] = useState('');

  // File Upload State
  const [selectedImageFile, setSelectedImageFile] = useState<File | null>(null);
  const [selectedVideoFile, setSelectedVideoFile] = useState<File | null>(null);
  const [uploadStatus, setUploadStatus] = useState<string | null>(null);
  const [uploadProgress, setUploadProgress] = useState<number | null>(null);

  // Status & Error Messages
  const [formError, setFormError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  // Pre-populate form when editingStretch changes
  useEffect(() => {
    if (editingStretch) {
      setName(editingStretch.name || '');
      setTargetArea(editingStretch.target_area || 'Hips & Glutes');
      setDescription(editingStretch.description || '');
      setDurationHint(editingStretch.duration_hint || '');
      setCaution(editingStretch.caution || '');
      setAliases(editingStretch.aliases || []);
    } else if (!isEditMode) {
      setName('');
      setTargetArea('Hips & Glutes');
      setDescription('');
      setDurationHint('');
      setCaution('');
      setAliases([]);
    }
  }, [editingStretch, isEditMode]);

  // Duplicate key collisions detection
  const existingKeyMap = useMemo(() => {
    const map = new Map<string, string>();
    for (const s of catalog) {
      if (editingStretch && s.id === editingStretch.id) continue;
      const nk = normalizeStretchKey(s.name);
      if (nk) map.set(nk, s.name);

      if (Array.isArray(s.aliases)) {
        for (const a of s.aliases) {
          const ak = normalizeStretchKey(a);
          if (ak) map.set(ak, a);
        }
      }
    }
    return map;
  }, [catalog, editingStretch]);

  const duplicateWarning = useMemo(() => {
    if (!name.trim()) return null;
    const nk = normalizeStretchKey(name);
    if (existingKeyMap.has(nk)) {
      return `Name "${name.trim()}" collides with existing entry: "${existingKeyMap.get(nk)}"`;
    }
    for (const a of aliases) {
      const ak = normalizeStretchKey(a);
      if (existingKeyMap.has(ak)) {
        return `Alias "${a}" collides with existing entry: "${existingKeyMap.get(ak)}"`;
      }
    }
    return null;
  }, [name, aliases, existingKeyMap]);

  const addAlias = () => {
    const trimmed = aliasInput.trim();
    if (!trimmed) return;
    if (aliases.some((a) => normalizeStretchKey(a) === normalizeStretchKey(trimmed))) {
      setAliasInput('');
      return;
    }
    setAliases([...aliases, trimmed]);
    setAliasInput('');
  };

  const removeAlias = (index: number) => {
    setAliases(aliases.filter((_, i) => i !== index));
  };

  const handleImageFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) {
      setSelectedImageFile(null);
      return;
    }

    if (!file.type.startsWith('image/')) {
      setFormError('Please select a valid image file for instruction image.');
      return;
    }

    if (file.size > MAX_IMAGE_SIZE) {
      setFormError(`Image file exceeds 10 MB limit (${(file.size / (1024 * 1024)).toFixed(1)} MB).`);
      return;
    }

    setFormError(null);
    setSelectedImageFile(file);
  };

  const handleVideoFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) {
      setSelectedVideoFile(null);
      return;
    }

    if (!file.type.startsWith('video/')) {
      setFormError('Please select a valid video file for instruction video.');
      return;
    }

    if (file.size > MAX_VIDEO_SIZE) {
      setFormError(`Video file exceeds 200 MB limit (${(file.size / (1024 * 1024)).toFixed(1)} MB).`);
      return;
    }

    setFormError(null);
    setSelectedVideoFile(file);
  };

  const clearMediaMutation = useMutation({
    mutationFn: ({ stretchId, kind }: { stretchId: number; kind?: 'image' | 'video' }) =>
      stretchesApi.clearMedia(stretchId, kind),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['stretches'] });
    },
  });

  const handleClearMedia = (kind?: 'image' | 'video') => {
    if (!editingStretch) return;
    const label = kind ? `${kind}` : 'all media';
    if (window.confirm(`Remove ${label} from "${editingStretch.name}"?`)) {
      clearMediaMutation.mutate({ stretchId: editingStretch.id, kind });
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setFormError('Name is required.');
      return;
    }

    setFormError(null);
    setIsSaving(true);

    try {
      const payload: StretchInput = {
        name: name.trim(),
        target_area: targetArea.trim(),
        description: description.trim(),
        duration_hint: durationHint.trim(),
        caution: caution.trim(),
        aliases,
      };

      let savedStretch: Stretch;
      if (isEditMode && editingStretch) {
        savedStretch = await stretchesApi.update(editingStretch.id, payload);
      } else {
        savedStretch = await stretchesApi.create(payload);
      }

      // 1. Upload Image file if selected
      if (selectedImageFile) {
        setUploadStatus('Uploading instruction image...');
        setUploadProgress(0);

        const { upload_url, object_name } = await stretchesApi.getMediaUploadUrl(
          savedStretch.id,
          selectedImageFile.name,
          'image',
        );

        await uploadToGCS(upload_url, selectedImageFile, (pct) => setUploadProgress(pct));
        savedStretch = await stretchesApi.setMedia(savedStretch.id, object_name, 'image');
      }

      // 2. Upload Video file if selected
      if (selectedVideoFile) {
        setUploadStatus('Uploading instruction video...');
        setUploadProgress(0);

        const { upload_url, object_name } = await stretchesApi.getMediaUploadUrl(
          savedStretch.id,
          selectedVideoFile.name,
          'video',
        );

        await uploadToGCS(upload_url, selectedVideoFile, (pct) => setUploadProgress(pct));
        savedStretch = await stretchesApi.setMedia(savedStretch.id, object_name, 'video');
      }

      await queryClient.invalidateQueries({ queryKey: ['stretches'] });
      navigate('/stretches/manage');
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to save stretch entry';
      setFormError(msg);
    } finally {
      setIsSaving(false);
      setUploadStatus(null);
      setUploadProgress(null);
    }
  };

  if (isEditMode && isLoadingCatalog) {
    return (
      <div className="max-w-3xl mx-auto py-12 space-y-4">
        <div className="h-6 bg-bg-tertiary rounded w-1/4 animate-pulse" />
        <div className="h-64 bg-bg-elevated border border-border rounded-2xl animate-pulse" />
      </div>
    );
  }

  if (isEditMode && !editingStretch) {
    return (
      <div className="max-w-3xl mx-auto py-12 text-center bg-bg-elevated border border-border rounded-2xl p-8 space-y-4">
        <h2 className="text-xl font-bold text-text-primary">Stretch Not Found</h2>
        <p className="text-xs text-text-secondary">
          The requested stretch catalog entry could not be found.
        </p>
        <Link
          to="/stretches/manage"
          className="inline-block px-4 py-2 bg-accent text-white rounded-xl text-xs font-semibold hover:bg-accent-hover transition-colors"
        >
          ← Back to Catalog Manager
        </Link>
      </div>
    );
  }

  return (
    <div className="max-w-3xl mx-auto space-y-6 pb-12">
      {/* Navigation Header */}
      <div>
        <Link
          to="/stretches/manage"
          className="inline-flex items-center gap-1.5 text-xs text-text-muted hover:text-accent transition-colors font-medium mb-2"
        >
          <span>←</span>
          <span>Back to Catalog Manager</span>
        </Link>
        <h1 className="text-2xl font-extrabold text-text-primary">
          {isEditMode && editingStretch ? `Edit Stretch: ${editingStretch.name}` : 'Create New Stretch'}
        </h1>
        <p className="text-xs text-text-secondary mt-1">
          {isEditMode
            ? 'Update stretch metadata, alternate aliases, and instruction media.'
            : 'Add a new canonical stretch entry to the system mobility catalog.'}
        </p>
      </div>

      {/* Main Form Container */}
      <div className="bg-bg-elevated border border-border rounded-2xl p-6 shadow-xl space-y-5">
        {formError && (
          <div role="alert" className="p-3.5 rounded-xl bg-error/10 border border-error/30 text-error text-xs">
            ⚠️ {formError}
          </div>
        )}

        {duplicateWarning && (
          <div className="p-3.5 rounded-xl bg-amber-500/10 border border-amber-500/30 text-amber-400 text-xs">
            ⚠️ {duplicateWarning}
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-5">
          <div className="grid gap-4 sm:grid-cols-2">
            {/* Stretch Name */}
            <div>
              <label className="block text-xs font-medium text-text-secondary mb-1.5">
                Stretch Name <span className="text-error">*</span>
              </label>
              <input
                type="text"
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. Pigeon Pose"
                className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2.5 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
              />
            </div>

            {/* Target Area */}
            <div>
              <label className="block text-xs font-medium text-text-secondary mb-1.5">
                Target Body Area
              </label>
              <input
                type="text"
                list="category-options"
                value={targetArea}
                onChange={(e) => setTargetArea(e.target.value)}
                placeholder="e.g. Hips & Glutes"
                className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2.5 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
              />
              <datalist id="category-options">
                {CATEGORY_OPTIONS.map((cat) => (
                  <option key={cat} value={cat} />
                ))}
              </datalist>
            </div>
          </div>

          {/* Description */}
          <div>
            <label className="block text-xs font-medium text-text-secondary mb-1.5">
              Instruction Description
            </label>
            <textarea
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Explain correct positioning, stretch targets, and posture cues..."
              className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2.5 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
            />
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            {/* Duration Hint */}
            <div>
              <label className="block text-xs font-medium text-text-secondary mb-1.5">
                Duration Hint
              </label>
              <input
                type="text"
                value={durationHint}
                onChange={(e) => setDurationHint(e.target.value)}
                placeholder="e.g. 90s per side"
                className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2.5 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
              />
            </div>

            {/* Caution */}
            <div>
              <label className="block text-xs font-medium text-text-secondary mb-1.5">
                Safety Caution
              </label>
              <input
                type="text"
                value={caution}
                onChange={(e) => setCaution(e.target.value)}
                placeholder="e.g. Protect knee — avoid sharp rotational force"
                className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2.5 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
              />
            </div>
          </div>

          {/* Aliases Chip Input */}
          <div>
            <label className="block text-xs font-medium text-text-secondary mb-1.5">
              Alternate Aliases (For LLM matching & deduplication)
            </label>
            <div className="flex items-center gap-2 mb-2">
              <input
                type="text"
                value={aliasInput}
                onChange={(e) => setAliasInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ',') {
                    e.preventDefault();
                    addAlias();
                  }
                }}
                placeholder="Type an alias and press Enter..."
                className="flex-1 bg-bg-secondary border border-border rounded-xl px-3.5 py-2.5 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
              />
              <button
                type="button"
                onClick={addAlias}
                className="px-3.5 py-2.5 bg-bg-tertiary hover:bg-border text-text-primary rounded-xl text-xs font-semibold transition-colors cursor-pointer"
              >
                Add Alias
              </button>
            </div>

            {aliases.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {aliases.map((alias, idx) => (
                  <span
                    key={idx}
                    className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-accent/10 border border-accent/20 text-accent text-xs font-medium"
                  >
                    <span>{alias}</span>
                    <button
                      type="button"
                      onClick={() => removeAlias(idx)}
                      className="hover:text-error transition-colors cursor-pointer"
                    >
                      ✕
                    </button>
                  </span>
                ))}
              </div>
            )}
          </div>

          {/* Media Upload Section */}
          <div className="border-t border-border pt-5 space-y-4">
            <h3 className="text-xs font-bold text-text-primary uppercase tracking-wider">
              Instruction Media (Image & Video)
            </h3>

            <div className="grid gap-4 sm:grid-cols-2">
              {/* 1. Image Upload & Preview */}
              <div className="p-4 rounded-xl bg-bg-secondary border border-border space-y-3">
                <label className="block text-xs font-medium text-text-primary flex items-center justify-between">
                  <span>🖼️ Instruction Image</span>
                  <span className="text-[10px] text-text-muted">Max 10 MB</span>
                </label>

                {editingStretch?.image_url && !selectedImageFile && (
                  <div className="space-y-2">
                    <img
                      src={editingStretch.image_url}
                      alt={`${editingStretch.name} Image`}
                      className="w-full h-36 object-cover rounded-lg border border-border"
                    />
                    <button
                      type="button"
                      onClick={() => handleClearMedia('image')}
                      className="w-full py-1.5 rounded-lg bg-error/10 hover:bg-error/20 text-error text-xs font-semibold transition-colors cursor-pointer"
                    >
                      Remove Image
                    </button>
                  </div>
                )}

                <input
                  type="file"
                  accept="image/*"
                  onChange={handleImageFileChange}
                  className="block w-full text-xs text-text-secondary file:mr-2 file:py-1.5 file:px-3 file:rounded-lg file:border-0 file:text-xs file:font-semibold file:bg-accent/10 file:text-accent hover:file:bg-accent/20 cursor-pointer"
                />
                {selectedImageFile && (
                  <div className="text-[11px] text-accent font-medium">
                    Selected: {selectedImageFile.name}
                  </div>
                )}
              </div>

              {/* 2. Video Upload & Preview */}
              <div className="p-4 rounded-xl bg-bg-secondary border border-border space-y-3">
                <label className="block text-xs font-medium text-text-primary flex items-center justify-between">
                  <span>🎥 Instruction Video</span>
                  <span className="text-[10px] text-text-muted">Max 200 MB</span>
                </label>

                {editingStretch?.video_url && !selectedVideoFile && (
                  <div className="space-y-2">
                    <video
                      src={editingStretch.video_url}
                      controls
                      preload="metadata"
                      className="w-full h-36 object-contain rounded-lg border border-border bg-black"
                    />
                    <button
                      type="button"
                      onClick={() => handleClearMedia('video')}
                      className="w-full py-1.5 rounded-lg bg-error/10 hover:bg-error/20 text-error text-xs font-semibold transition-colors cursor-pointer"
                    >
                      Remove Video
                    </button>
                  </div>
                )}

                <input
                  type="file"
                  accept="video/*"
                  onChange={handleVideoFileChange}
                  className="block w-full text-xs text-text-secondary file:mr-2 file:py-1.5 file:px-3 file:rounded-lg file:border-0 file:text-xs file:font-semibold file:bg-accent/10 file:text-accent hover:file:bg-accent/20 cursor-pointer"
                />
                {selectedVideoFile && (
                  <div className="text-[11px] text-accent font-medium">
                    Selected: {selectedVideoFile.name}
                  </div>
                )}
              </div>
            </div>

            {uploadProgress !== null && (
              <div className="space-y-1 p-3 rounded-xl bg-accent/5 border border-accent/20">
                <div className="flex justify-between text-xs text-text-primary font-medium">
                  <span>{uploadStatus || 'Uploading media file...'}</span>
                  <span>{uploadProgress}%</span>
                </div>
                <div className="w-full bg-bg-secondary h-2 rounded-full overflow-hidden">
                  <div
                    className="bg-accent h-full transition-all duration-200"
                    style={{ width: `${uploadProgress}%` }}
                  />
                </div>
              </div>
            )}
          </div>

          {/* Form Action Buttons */}
          <div className="flex justify-end gap-3 pt-3 border-t border-border">
            <button
              type="button"
              onClick={() => navigate('/stretches/manage')}
              disabled={isSaving}
              className="px-4 py-2.5 rounded-xl text-xs font-semibold bg-bg-secondary border border-border text-text-secondary hover:text-text-primary hover:bg-bg-tertiary transition-colors cursor-pointer"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isSaving}
              className="px-6 py-2.5 rounded-xl text-xs font-semibold bg-accent hover:bg-accent-hover text-white transition-all shadow-md shadow-accent/20 disabled:opacity-50 cursor-pointer"
            >
              {isSaving ? 'Saving...' : isEditMode ? 'Update Stretch' : 'Create Stretch'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
