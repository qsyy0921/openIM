import { AlertTriangle, Check, CircleDot, LogIn, LogOut, MessageSquare, RefreshCw, ShieldCheck, Wifi } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { User, UserManager } from "oidc-client-ts";

import type { WebConfig } from "./config";
import { connectOpenIM, disconnectOpenIM, type ConnectionUpdate } from "./openim";
import { createIMSession, type IMSession } from "./platform-api";

type Phase = "booting" | "signed-out" | "exchanging" | "connecting" | "connected" | "error";

type AppProps = {
  config: WebConfig;
  userManager: UserManager;
};

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : "unexpected client failure";
}

export function App({ config, userManager }: AppProps) {
  const [phase, setPhase] = useState<Phase>("booting");
  const [user, setUser] = useState<User | null>(null);
  const [session, setSession] = useState<IMSession | null>(null);
  const [error, setError] = useState<string | null>(null);
  const detachRef = useRef<(() => void) | null>(null);

  const connect = useCallback(
    async (identity: User) => {
      if (!identity.id_token || identity.expired) {
        await userManager.removeUser();
        setPhase("signed-out");
        return;
      }
      setError(null);
      setPhase("exchanging");
      try {
        const nextSession = await createIMSession(config.platformAPIBaseURL, identity.id_token, config.deviceID);
        setSession(nextSession);
        setPhase("connecting");
        detachRef.current?.();
        detachRef.current = await connectOpenIM(config, nextSession, (update: ConnectionUpdate) => {
          if (update.state === "connected") {
            setPhase("connected");
            return;
          }
          if (update.state === "connecting") {
            setPhase("connecting");
            return;
          }
          setError(update.message ?? update.state);
          setPhase("error");
        });
      } catch (cause) {
        setError(messageOf(cause));
        setPhase("error");
      }
    },
    [config, userManager]
  );

  useEffect(() => {
    let active = true;
    const initialize = async () => {
      try {
        const callback = window.location.pathname === new URL(config.oidcRedirectURI).pathname;
        const identity = callback ? await userManager.signinRedirectCallback() : await userManager.getUser();
        if (callback) {
          window.history.replaceState({}, document.title, "/");
        }
        if (!active) return;
        setUser(identity);
        if (!identity || identity.expired) {
          setPhase("signed-out");
          return;
        }
        await connect(identity);
      } catch (cause) {
        if (!active) return;
        setError(messageOf(cause));
        setPhase("error");
      }
    };
    void initialize();
    return () => {
      active = false;
      detachRef.current?.();
    };
  }, [config.oidcRedirectURI, connect, userManager]);

  useEffect(() => {
    const expired = () => {
      setError("Enterprise identity expired");
      setPhase("error");
    };
    userManager.events.addAccessTokenExpired(expired);
    return () => userManager.events.removeAccessTokenExpired(expired);
  }, [userManager]);

  const login = async () => {
    setError(null);
    try {
      await userManager.signinRedirect();
    } catch (cause) {
      setError(messageOf(cause));
      setPhase("error");
    }
  };

  const logout = async () => {
    setPhase("booting");
    setError(null);
    try {
      if (session) await disconnectOpenIM();
      detachRef.current?.();
      detachRef.current = null;
      await userManager.signoutRedirect();
    } catch (cause) {
      setError(messageOf(cause));
      setPhase("error");
    }
  };

  const retry = async () => {
    if (!user) {
      setPhase("signed-out");
      return;
    }
    await connect(user);
  };

  const displayName = useMemo(() => {
    const profile = user?.profile;
    return String(profile?.name ?? profile?.preferred_username ?? profile?.sub ?? "Enterprise member");
  }, [user]);

  if (phase === "signed-out") {
    return (
      <main className="auth-shell">
        <section className="auth-panel" aria-labelledby="sign-in-title">
          <div className="brand-mark"><MessageSquare size={22} /></div>
          <div>
            <p className="eyebrow">OPENIM WORKSPACE</p>
            <h1 id="sign-in-title">企业协作台</h1>
          </div>
          <button className="primary-button" onClick={() => void login()}><LogIn size={18} />企业登录</button>
        </section>
      </main>
    );
  }

  const progress = phase === "exchanging" ? "正在签发 IM 会话" : phase === "connecting" ? "正在连接 OpenIM" : "正在恢复会话";
  if (phase === "booting" || phase === "exchanging" || phase === "connecting") {
    return (
      <main className="loading-shell" aria-live="polite">
        <RefreshCw className="spin" size={24} />
        <span>{progress}</span>
      </main>
    );
  }

  return (
    <div className="workspace-shell">
      <aside className="rail">
        <div className="brand-mark small"><MessageSquare size={19} /></div>
        <button className="rail-button active" title="连接状态" aria-label="连接状态"><Wifi size={19} /></button>
      </aside>
      <main className="workspace-main">
        <header className="workspace-header">
          <div>
            <p className="eyebrow">OPENIM WORKSPACE</p>
            <h1>连接控制台</h1>
          </div>
          <button className="icon-text-button" onClick={() => void logout()}><LogOut size={17} />退出</button>
        </header>

        <section className="status-band" aria-live="polite">
          <div className={`status-icon ${phase === "connected" ? "success" : "danger"}`}>
            {phase === "connected" ? <Check size={24} /> : <AlertTriangle size={24} />}
          </div>
          <div>
            <p className="status-label">实时通信</p>
            <h2>{phase === "connected" ? "OpenIM 已连接" : "连接失败"}</h2>
            {error && <p className="error-text">{error}</p>}
          </div>
          {phase === "error" && <button className="secondary-button" onClick={() => void retry()}><RefreshCw size={17} />重试</button>}
        </section>

        <section className="detail-grid">
          <article className="detail-block">
            <div className="detail-heading"><ShieldCheck size={18} /><span>企业身份</span></div>
            <strong>{displayName}</strong>
            <span className="muted">{String(user?.profile.email ?? user?.profile.sub ?? "")}</span>
          </article>
          <article className="detail-block">
            <div className="detail-heading"><CircleDot size={18} /><span>IM 用户</span></div>
            <strong>{session?.userID ?? "未签发"}</strong>
            <span className="muted">Token 到期 {session ? new Date(session.expiresAt).toLocaleString() : "-"}</span>
          </article>
        </section>

        <section className="endpoint-table" aria-label="运行端点">
          <div><span>Platform API</span><code>{config.platformAPIBaseURL}</code><b>READY</b></div>
          <div><span>OpenIM API</span><code>{config.openIMAPIURL}</code><b>READY</b></div>
          <div><span>WebSocket</span><code>{config.openIMWSURL}</code><b>{phase === "connected" ? "OPEN" : "CLOSED"}</b></div>
        </section>
      </main>
    </div>
  );
}
