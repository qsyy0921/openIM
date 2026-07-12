import type { LucideIcon } from "lucide-react";
import { LogOut } from "lucide-react";
import type { ReactNode } from "react";

export type WorkspaceModule = {
  id: string;
  label: string;
  icon: LucideIcon;
};

type WorkspaceShellProps = {
  activeModule: string;
  modules: WorkspaceModule[];
  displayName: string;
  onModuleSelect: (moduleID: string) => void;
  onLogout: () => void;
  children: ReactNode;
};

function initials(displayName: string): string {
  const value = displayName.trim();
  return value ? value.slice(0, 1).toUpperCase() : "U";
}

export function WorkspaceShell({ activeModule, modules, displayName, onModuleSelect, onLogout, children }: WorkspaceShellProps) {
  return (
    <div className="workspace-shell chat-shell">
      <aside className="workspace-nav" aria-label="工作区导航">
        <div className="workspace-brand" aria-label="OpenIM 协作平台">O</div>
        <nav className="module-nav">
          {modules.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              className={`module-button ${activeModule === id ? "active" : ""}`}
              aria-current={activeModule === id ? "page" : undefined}
              aria-label={label}
              title={label}
              onClick={() => onModuleSelect(id)}
            >
              <Icon size={21} strokeWidth={1.8} />
              <span>{label}</span>
            </button>
          ))}
        </nav>
        <div className="workspace-account">
          <span className="account-avatar" title={displayName}>{initials(displayName)}</span>
          <button className="account-action" onClick={onLogout} aria-label="退出" title="退出">
            <LogOut size={19} strokeWidth={1.8} />
          </button>
        </div>
      </aside>
      <main className="workspace-content">{children}</main>
    </div>
  );
}
