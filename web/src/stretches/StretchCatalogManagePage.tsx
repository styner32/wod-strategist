import { useState, useMemo } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { stretchesApi, type Stretch } from '../api/stretches';

export function StretchCatalogManagePage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [searchQuery, setSearchQuery] = useState('');

  const { data: catalog = [], isLoading, error } = useQuery({
    queryKey: ['stretches'],
    queryFn: stretchesApi.list,
    staleTime: 10 * 60 * 1000,
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => stretchesApi.remove(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['stretches'] });
    },
  });

  const handleDelete = (s: Stretch) => {
    if (window.confirm(`Are you sure you want to delete "${s.name}"? This action cannot be undone.`)) {
      deleteMutation.mutate(s.id);
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
          onClick={() => navigate('/stretches/manage/new')}
          className="px-4 py-2.5 bg-accent hover:bg-accent-hover text-white rounded-xl text-xs font-semibold shadow-md shadow-accent/20 transition-all self-start sm:self-auto flex items-center gap-1.5 cursor-pointer"
        >
          <span>＋</span>
          <span>Add Stretch</span>
        </button>
      </div>

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
                  onClick={() => navigate(`/stretches/manage/${stretch.id}`)}
                  className="px-3.5 py-1.5 rounded-lg bg-bg-secondary hover:bg-bg-tertiary border border-border text-text-primary text-xs font-medium transition-colors cursor-pointer"
                >
                  Edit
                </button>
                <button
                  onClick={() => handleDelete(stretch)}
                  className="px-3.5 py-1.5 rounded-lg bg-error/10 hover:bg-error/20 text-error text-xs font-semibold transition-colors cursor-pointer"
                >
                  Delete
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {!isLoading && filteredCatalog.length === 0 && (
        <div className="text-center py-12 bg-bg-elevated border border-border rounded-2xl p-6">
          <p className="text-sm text-text-secondary">No catalog entries found matching "{searchQuery}".</p>
          <button
            onClick={() => setSearchQuery('')}
            className="mt-3 text-xs text-accent hover:underline font-medium cursor-pointer"
          >
            Clear Search
          </button>
        </div>
      )}
    </div>
  );
}
