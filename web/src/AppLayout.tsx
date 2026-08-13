import { Outlet, Link, useLocation } from 'react-router-dom';
import { useAuth } from './auth/useAuth';

function NavLink({ to, children }: { to: string; children: React.ReactNode }) {
  const location = useLocation();
  const isActive = location.pathname === to;
  return (
    <Link
      to={to}
      className={`px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
        isActive
          ? 'bg-accent/10 text-accent'
          : 'text-text-secondary hover:text-text-primary hover:bg-bg-tertiary'
      }`}
    >
      {children}
    </Link>
  );
}

export function AppLayout() {
  const { user, logout } = useAuth();

  return (
    <div className="min-h-screen flex flex-col">
      {/* Header */}
      <header className="border-b border-border bg-bg-secondary/50 backdrop-blur-sm sticky top-0 z-50">
        <div className="max-w-7xl mx-auto px-6 h-14 flex items-center justify-between">
          <div className="flex items-center gap-6">
            <Link to="/" className="text-lg font-bold bg-gradient-to-r from-accent to-purple-400 bg-clip-text text-transparent">
              WOD Strategist
            </Link>
            <nav className="flex items-center gap-1">
              <NavLink to="/">History</NavLink>
              <NavLink to="/stretches">Stretches</NavLink>
              <NavLink to="/upload">Upload</NavLink>
            </nav>
          </div>

          <div className="flex items-center gap-4">
            <span className="text-sm text-text-muted">
              {user?.username}
            </span>
            <button
              onClick={logout}
              className="text-sm text-text-secondary hover:text-text-primary transition-colors cursor-pointer"
            >
              Sign out
            </button>
          </div>
        </div>
      </header>

      {/* Main content */}
      <main className="flex-1 px-6 py-8">
        <Outlet />
      </main>

      {/* Footer */}
      <footer className="border-t border-border py-4">
        <p className="text-center text-xs text-text-muted">
          WOD Strategist — AI-Powered Workout Analysis
        </p>
      </footer>
    </div>
  );
}
