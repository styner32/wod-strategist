import { useState, useMemo } from 'react';
import { Link } from 'react-router-dom';
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

export function StretchEditorPage() {
  const queryClient = useQueryClient();

  const { data: catalog = [], isLoading, error } = useQuery({
    queryKey: ['stretches'],
    queryFn: stretchesApi.list,
    staleTime: 10 * 60 * 1000,
  });

  const [editingStretch, setEditingStretch] = useState<Stretch | null>(null);
  const [isFormOpen, setIsFormOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');

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

  // Error / Status Message
  const [formError, setFormError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  // Existing normalized keys in catalog (excluding item being edited)
  const existingKeyMap = useMemo(() => {
    const map = new Map<string, string>(); // normalizedKey -> originalLabel
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

  // Client-side duplicate check warnings
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

  const openCreateForm = () => {
    setEditingStretch(null);
    setName('');
    setTargetArea('Hips & Glutes');
    setDescription('');
    setDurationHint('');
    setCaution('');
    setAliases([]);
    setAliasInput('');
    setSelectedImageFile(null);
    setSelectedVideoFile(null);
    setUploadStatus(null);
    setUploadProgress(null);
    setFormError(null);
    setIsFormOpen(true);
  };

  const openEditForm = (stretch: Stretch) => {
    setEditingStretch(stretch);
    setName(stretch.name);
    setTargetArea(stretch.target_area || 'Hips & Glutes');
    setDescription(stretch.description || '');
    setDurationHint(stretch.duration_hint || '');
    setCaution(stretch.caution || '');
    setAliases(stretch.aliases || []);
    setAliasInput('');
    setSelectedImageFile(null);
    setSelectedVideoFile(null);
    setUploadStatus(null);
    setUploadProgress(null);
    setFormError(null);
    setIsFormOpen(true);
  };

  const closeForm = () => {
    setIsFormOpen(false);
    setEditingStretch(null);
    setFormError(null);
    setSelectedImageFile(null);
    setSelectedVideoFile(null);
    setUploadStatus(null);
    setUploadProgress(null);
  };

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
      setFormError('Please select a valid image file for the instruction image.');
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
      setFormError('Please select a valid video file for the instruction video.');
      return;
    }

    if (file.size > MAX_VIDEO_SIZE) {
      setFormError(`Video file exceeds 200 MB limit (${(file.size / (1024 * 1024)).toFixed(1)} MB).`);
      return;
    }

    setFormError(null);
    setSelectedVideoFile(file);
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
      if (editingStretch) {
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
      closeForm();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to save stretch entry';
      setFormError(msg);
    } finally {
      setIsSaving(false);
      setUploadStatus(null);
      setUploadProgress(null);
    }
  };

  const deleteMutation = useMutation({
    mutationFn: (id: number) => stretchesApi.remove(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['stretches'] });
    },
  });

  const clearMediaMutation = useMutation({
    mutationFn: ({ id, kind }: { id: number; kind?: 'image' | 'video' }) =>
      stretchesApi.clearMedia(id, kind),
    onSuccess: (updatedStretch) => {
      queryClient.invalidateQueries({ queryKey: ['stretches'] });
      if (editingStretch && editingStretch.id === updatedStretch.id) {
        setEditingStretch(updatedStretch);
      }
    },
  });

  const handleDelete = (s: Stretch) => {
    if (window.confirm(`Are you sure you want to delete "${s.name}"? This action cannot be undone.`)) {
      deleteMutation.mutate(s.id);
    }
  };

  const handleClearMedia = (s: Stretch, kind?: 'image' | 'video') => {
    const label = kind ? `${kind}` : 'all media';
    if (window.confirm(`Remove ${label} from "${s.name}"?`)) {
      clearMediaMutation.mutate({ id: s.id, kind });
    }
  };

  const filteredCatalog = useMemo(() => {
    if (!searchQuery.trim()) return catalog;
    const q = searchQuery.toLowerCase();
    return catalog.filter((s) => {
      return (
        s.name.toLowerCase().includes(q) ||
        s.target_area.toLowerCase().includes(q) ||
        s.description.toLowerCase().includes(q) ||
        (s.aliases && s.aliases.some((a) => a.toLowerCase().includes(q)))
      );
    });
  }, [catalog, searchQuery]);

  return (
    <div className="max-w-6xl mx-auto space-y-8 pb-12">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-border pb-5">
        <div>
          <div className="flex items-center gap-2 text-xs text-text-muted mb-1">
            <Link to="/stretches" className="hover:text-accent transition-colors">
              ← Back to Stretches
            </Link>
          </div>
          <h1 className="text-2xl font-extrabold text-text-primary">Stretch Catalog Manager</h1>
          <p className="text-xs text-text-secondary mt-1">
            Create and edit canonical stretches, manage alternate alias names, and attach instruction images and videos.
          </p>
        </div>

        <button
          onClick={openCreateForm}
          className="px-4 py-2.5 bg-accent hover:bg-accent-hover text-white rounded-xl text-xs font-semibold shadow-md shadow-accent/20 transition-all self-start sm:self-auto flex items-center gap-1.5"
        >
          <span>＋</span>
          <span>Add Stretch</span>
        </button>
      </div>

      {/* Inline Create / Edit Form Card */}
      {isFormOpen && (
        <div className="bg-bg-elevated border border-accent/40 rounded-2xl p-6 shadow-xl relative animate-in fade-in zoom-in-95 duration-200">
          <h2 className="text-lg font-bold text-text-primary mb-4">
            {editingStretch ? `Edit Stretch: ${editingStretch.name}` : 'Create New Stretch'}
          </h2>

          {formError && (
            <div role="alert" className="mb-4 p-3 rounded-xl bg-error/10 border border-error/30 text-error text-xs">
              ⚠️ {formError}
            </div>
          )}

          {duplicateWarning && (
            <div className="mb-4 p-3 rounded-xl bg-amber-500/10 border border-amber-500/30 text-amber-400 text-xs">
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
                  className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
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
                  className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
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
                className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
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
                  className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
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
                  className="w-full bg-bg-secondary border border-border rounded-xl px-3.5 py-2 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
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
                  className="flex-1 bg-bg-secondary border border-border rounded-xl px-3.5 py-2 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
                />
                <button
                  type="button"
                  onClick={addAlias}
                  className="px-3.5 py-2 bg-bg-tertiary hover:bg-border text-text-primary rounded-xl text-xs font-semibold transition-colors"
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
                        className="hover:text-error transition-colors"
                      >
                        ✕
                      </button>
                    </span>
                  ))}
                </div>
              )}
            </div>

            {/* Media Upload Section: Dual Image and Video Upload */}
            <div className="border-t border-border pt-4 space-y-4">
              <h3 className="text-xs font-bold text-text-primary uppercase tracking-wider">
                Instruction Media (Both Image & Video Supported)
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
                        className="w-full h-32 object-cover rounded-lg border border-border"
                      />
                      <button
                        type="button"
                        onClick={() => handleClearMedia(editingStretch, 'image')}
                        className="w-full py-1.5 rounded-lg bg-error/10 hover:bg-error/20 text-error text-xs font-semibold transition-colors"
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
                        className="w-full h-32 object-contain rounded-lg border border-border bg-black"
                      />
                      <button
                        type="button"
                        onClick={() => handleClearMedia(editingStretch, 'video')}
                        className="w-full py-1.5 rounded-lg bg-error/10 hover:bg-error/20 text-error text-xs font-semibold transition-colors"
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
            <div className="flex justify-end gap-3 pt-2">
              <button
                type="button"
                onClick={closeForm}
                disabled={isSaving}
                className="px-4 py-2 rounded-xl text-xs font-semibold bg-bg-secondary border border-border text-text-secondary hover:text-text-primary hover:bg-bg-tertiary transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={isSaving}
                className="px-5 py-2 rounded-xl text-xs font-semibold bg-accent hover:bg-accent-hover text-white transition-all shadow-md shadow-accent/20 disabled:opacity-50"
              >
                {isSaving ? 'Saving...' : editingStretch ? 'Update Stretch' : 'Create Stretch'}
              </button>
            </div>
          </form>
        </div>
      )}

      {/* Catalog Search & Count */}
      <div className="flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-4">
        <div className="text-xs text-text-muted">
          Showing <span className="font-semibold text-text-primary">{filteredCatalog.length}</span> of {catalog.length} catalog entries
        </div>

        <input
          type="text"
          placeholder="Search stretches or aliases..."
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          className="min-w-[240px] bg-bg-elevated border border-border rounded-xl px-4 py-2 text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-2 focus:ring-accent/50"
        />
      </div>

      {/* Loading & Error States */}
      {isLoading && (
        <div className="grid gap-4 md:grid-cols-2">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="bg-bg-elevated border border-border rounded-2xl p-5 animate-pulse space-y-3">
              <div className="h-5 bg-bg-tertiary rounded w-1/3" />
              <div className="h-4 bg-bg-tertiary rounded w-2/3" />
              <div className="h-10 bg-bg-tertiary rounded w-full" />
            </div>
          ))}
        </div>
      )}

      {error && (
        <div className="bg-error/10 border border-error/30 text-error rounded-xl p-4 text-xs">
          Failed to load stretch catalog. Please refresh the page.
        </div>
      )}

      {/* Catalog Cards Grid */}
      {!isLoading && filteredCatalog.length > 0 && (
        <div className="grid gap-4 md:grid-cols-2">
          {filteredCatalog.map((stretch) => (
            <div
              key={stretch.id}
              className="bg-bg-elevated border border-border rounded-2xl p-5 flex flex-col justify-between hover:border-accent/30 transition-all duration-200 shadow-sm"
            >
              <div>
                {/* Media Thumbnails & Video Player */}
                {stretch.image_url || stretch.video_url ? (
                  <div className="mb-4 grid gap-2 sm:grid-cols-2">
                    {stretch.image_url && (
                      <div className="overflow-hidden rounded-xl border border-border bg-black h-40 flex items-center justify-center">
                        <img
                          src={stretch.image_url}
                          alt={`${stretch.name} Image`}
                          className="w-full h-40 object-cover rounded-xl"
                        />
                      </div>
                    )}
                    {stretch.video_url && (
                      <div className="overflow-hidden rounded-xl border border-border bg-black h-40 flex items-center justify-center">
                        <video
                          src={stretch.video_url}
                          controls
                          preload="metadata"
                          className="w-full h-40 object-contain rounded-xl"
                        />
                      </div>
                    )}
                  </div>
                ) : null}

                {/* Header */}
                <div className="flex items-center justify-between gap-2 mb-2">
                  <span className="text-xs font-semibold px-2.5 py-0.5 rounded-md bg-accent/10 text-accent border border-accent/20">
                    {stretch.target_area || 'General'}
                  </span>
                  {stretch.duration_hint && (
                    <span className="text-[11px] text-text-muted font-medium">
                      ⏱️ {stretch.duration_hint}
                    </span>
                  )}
                </div>

                <h3 className="text-base font-bold text-text-primary mb-1">{stretch.name}</h3>

                {stretch.description && (
                  <p className="text-xs text-text-secondary leading-relaxed mb-3">
                    {stretch.description}
                  </p>
                )}

                {stretch.caution && (
                  <div className="p-2.5 rounded-lg bg-amber-500/10 border border-amber-500/20 text-amber-400 text-[11px] leading-snug mb-3">
                    ⚠️ {stretch.caution}
                  </div>
                )}

                {/* Aliases */}
                {stretch.aliases && stretch.aliases.length > 0 && (
                  <div className="flex flex-wrap gap-1 mt-2">
                    <span className="text-[10px] text-text-muted font-medium self-center mr-1">
                      Aliases:
                    </span>
                    {stretch.aliases.map((alias, i) => (
                      <span
                        key={i}
                        className="text-[10px] px-2 py-0.5 rounded bg-bg-secondary border border-border text-text-secondary"
                      >
                        {alias}
                      </span>
                    ))}
                  </div>
                )}
              </div>

              {/* Card Actions */}
              <div className="mt-5 pt-3 border-t border-border-subtle flex items-center justify-end gap-2">
                <button
                  onClick={() => openEditForm(stretch)}
                  className="px-3 py-1.5 rounded-lg bg-bg-secondary hover:bg-bg-tertiary border border-border text-text-primary text-xs font-medium transition-colors"
                >
                  Edit
                </button>
                <button
                  onClick={() => handleDelete(stretch)}
                  className="px-3 py-1.5 rounded-lg bg-error/10 hover:bg-error/20 text-error text-xs font-semibold transition-colors"
                >
                  Delete
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
