import { AlertTriangle, Bot, ContactRound, LogIn, LogOut, MessageCircle, MessageSquare, MonitorSmartphone, RefreshCw, Wifi } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { User, UserManager } from "oidc-client-ts";
import { ApplicationHandleResult } from "@openim/wasm-client-sdk";

import type { WebConfig } from "./config";
import { AgentWorkspace } from "./AgentWorkspace";
import { AgentController, createOpenIMAgentTransport, initialAgentState, type AgentState } from "./agent";
import { approveAgentIntent, getAgentCatalog, getAgentWorkspace } from "./agent-api";
import { ChatWorkspace } from "./ChatWorkspace";
import { ConversationController, createOpenIMChatPort, initialChatState, type ChatState } from "./chat";
import { ContactController, createOpenIMContactPort, initialContactState, type ContactState } from "./contact";
import { ContactsWorkspace } from "./ContactsWorkspace";
import { DeviceWorkspace } from "./DeviceWorkspace";
import { DeviceController, initialDeviceState, type DeviceState } from "./device";
import { getDevices, logoutPlatform } from "./device-api";
import { createOpenIMMessageSearchPort, initialMessageSearchState, MessageSearchController, type MessageSearchState } from "./message-search";
import { connectOpenIM, disconnectOpenIM, type ConnectionUpdate } from "./openim";
import { createIMSession, type IMSession } from "./platform-api";
import { WorkspaceShell, type WorkspaceModule } from "./WorkspaceShell";

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
  const [connection, setConnection] = useState<ConnectionUpdate>({ state: "connecting" });
  const [chatState, setChatState] = useState<ChatState>(initialChatState);
  const [contactState, setContactState] = useState<ContactState>(initialContactState);
  const [messageSearchState, setMessageSearchState] = useState<MessageSearchState>(initialMessageSearchState);
  const [deviceState, setDeviceState] = useState<DeviceState>(initialDeviceState);
  const [agentState, setAgentState] = useState<AgentState>(initialAgentState);
  const [activeModule, setActiveModule] = useState("messages");
  const detachRef = useRef<(() => void) | null>(null);
  const chatControllerRef = useRef<ConversationController | null>(null);
  const unsubscribeChatRef = useRef<(() => void) | null>(null);
  const contactControllerRef = useRef<ContactController | null>(null);
  const unsubscribeContactRef = useRef<(() => void) | null>(null);
  const messageSearchControllerRef = useRef<MessageSearchController | null>(null);
  const unsubscribeMessageSearchRef = useRef<(() => void) | null>(null);
  const deviceControllerRef = useRef<DeviceController | null>(null);
  const unsubscribeDeviceRef = useRef<(() => void) | null>(null);
  const deviceStartedRef = useRef(false);
  const agentControllerRef = useRef<AgentController | null>(null);
  const unsubscribeAgentRef = useRef<(() => void) | null>(null);
  const agentStartedRef = useRef(false);

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
          setConnection(update);
          if (update.state === "connected") {
            const controller = chatControllerRef.current;
            if (controller) {
              void Promise.all([controller.restore(), contactControllerRef.current?.restore()]).then(() => setPhase("connected")).catch((cause) => {
                setError(messageOf(cause));
                setConnection({ state: "failed", message: messageOf(cause) });
              });
            }
            if (agentStartedRef.current) {
              void agentControllerRef.current?.start().catch((cause) => setError(messageOf(cause)));
            }
            return;
          }
          if (update.state === "connecting") {
            if (!chatControllerRef.current) setPhase("connecting");
            return;
          }
          if (update.state === "kicked" || update.state === "expired") {
            chatControllerRef.current?.stop();
            contactControllerRef.current?.stop();
            messageSearchControllerRef.current?.close();
            deviceControllerRef.current?.close();
            agentControllerRef.current?.stop();
            setSession(null);
            setError(update.message ?? update.state);
            setPhase("error");
            return;
          }
          if (!chatControllerRef.current) {
            setError(update.message ?? update.state);
            setPhase("error");
          }
        });
        chatControllerRef.current?.stop();
        unsubscribeChatRef.current?.();
        const controller = new ConversationController(createOpenIMChatPort());
        chatControllerRef.current = controller;
        unsubscribeChatRef.current = controller.subscribe(setChatState);
        await controller.start(nextSession.userID);
        messageSearchControllerRef.current?.close();
        unsubscribeMessageSearchRef.current?.();
        const messageSearchController = new MessageSearchController(createOpenIMMessageSearchPort());
        messageSearchControllerRef.current = messageSearchController;
        unsubscribeMessageSearchRef.current = messageSearchController.subscribe(setMessageSearchState);
        contactControllerRef.current?.stop();
        unsubscribeContactRef.current?.();
        const contactController = new ContactController(createOpenIMContactPort());
        contactControllerRef.current = contactController;
        unsubscribeContactRef.current = contactController.subscribe(setContactState);
        await contactController.start(nextSession.userID);
        deviceControllerRef.current?.close();
        unsubscribeDeviceRef.current?.();
        const deviceController = new DeviceController({
          list: () => getDevices(config.platformAPIBaseURL, identity.id_token!, config.deviceID),
          logout: (platformID) => logoutPlatform(config.platformAPIBaseURL, identity.id_token!, config.deviceID, platformID)
        });
        deviceControllerRef.current = deviceController;
        unsubscribeDeviceRef.current = deviceController.subscribe(setDeviceState);
        deviceStartedRef.current = false;
        agentControllerRef.current?.stop();
        unsubscribeAgentRef.current?.();
        const agentController = new AgentController({
          catalog: () => getAgentCatalog(config.platformAPIBaseURL, identity.id_token!, config.deviceID),
          workspace: () => getAgentWorkspace(config.platformAPIBaseURL, identity.id_token!, config.deviceID),
          approve: (intentID, digest) => approveAgentIntent(config.platformAPIBaseURL, identity.id_token!, config.deviceID, intentID, digest)
        }, createOpenIMAgentTransport());
        agentControllerRef.current = agentController;
        unsubscribeAgentRef.current = agentController.subscribe(setAgentState);
        agentStartedRef.current = false;
        setConnection({ state: "connected" });
        setPhase("connected");
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
      chatControllerRef.current?.stop();
      unsubscribeChatRef.current?.();
      contactControllerRef.current?.stop();
      unsubscribeContactRef.current?.();
      messageSearchControllerRef.current?.close();
      unsubscribeMessageSearchRef.current?.();
      deviceControllerRef.current?.close();
      unsubscribeDeviceRef.current?.();
      agentControllerRef.current?.stop();
      unsubscribeAgentRef.current?.();
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
      chatControllerRef.current?.stop();
      chatControllerRef.current = null;
      unsubscribeChatRef.current?.();
      unsubscribeChatRef.current = null;
      contactControllerRef.current?.stop();
      contactControllerRef.current = null;
      unsubscribeContactRef.current?.();
      unsubscribeContactRef.current = null;
      messageSearchControllerRef.current?.close();
      messageSearchControllerRef.current = null;
      unsubscribeMessageSearchRef.current?.();
      unsubscribeMessageSearchRef.current = null;
      deviceControllerRef.current?.close();
      deviceControllerRef.current = null;
      unsubscribeDeviceRef.current?.();
      unsubscribeDeviceRef.current = null;
      deviceStartedRef.current = false;
      agentControllerRef.current?.stop();
      agentControllerRef.current = null;
      unsubscribeAgentRef.current?.();
      unsubscribeAgentRef.current = null;
      agentStartedRef.current = false;
      setChatState(initialChatState);
      setContactState(initialContactState);
      setMessageSearchState(initialMessageSearchState);
      setDeviceState(initialDeviceState);
      setAgentState(initialAgentState);
      setActiveModule("messages");
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

  const workspaceModules = useMemo<WorkspaceModule[]>(() => {
    const pendingContacts = contactState.incomingApplications.filter((application) => application.handleResult === ApplicationHandleResult.Unprocessed).length;
    return [
      { id: "messages", label: "消息", icon: MessageCircle },
      { id: "contacts", label: "通讯录", icon: ContactRound, badge: pendingContacts },
      { id: "devices", label: "设备", icon: MonitorSmartphone },
      { id: "agent", label: "智能助手", icon: Bot }
    ];
  }, [contactState.incomingApplications]);

  const selectModule = async (moduleID: string) => {
    if (moduleID !== "messages" && moduleID !== "contacts" && moduleID !== "devices" && moduleID !== "agent") return;
    setActiveModule(moduleID);
    if (moduleID === "devices" && !deviceStartedRef.current) {
      deviceStartedRef.current = true;
      try {
        await deviceControllerRef.current?.start();
      } catch {
        // DeviceState contains the explicit initialization error.
      }
    }
    if (moduleID === "agent" && !agentStartedRef.current) {
      agentStartedRef.current = true;
      try {
        await agentControllerRef.current?.start();
      } catch {
        // AgentState contains the explicit initialization error.
      }
    }
  };

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

  if (phase === "connected" && session && chatControllerRef.current && contactControllerRef.current && messageSearchControllerRef.current && deviceControllerRef.current) {
    const openContactChat = async (userID: string) => {
      await chatControllerRef.current!.openDirect(userID);
      setActiveModule("messages");
    };
    return (
      <WorkspaceShell activeModule={activeModule} modules={workspaceModules} displayName={displayName} onModuleSelect={(moduleID) => void selectModule(moduleID)} onLogout={() => void logout()}>
        {activeModule === "messages" ? (
          <ChatWorkspace controller={chatControllerRef.current} state={chatState} selfUserID={session.userID} connection={connection} contactController={contactControllerRef.current} contactState={contactState} searchController={messageSearchControllerRef.current} searchState={messageSearchState} />
        ) : activeModule === "contacts" ? (
          <ContactsWorkspace controller={contactControllerRef.current} state={contactState} onOpenChat={openContactChat} />
        ) : activeModule === "devices" ? (
          <DeviceWorkspace controller={deviceControllerRef.current} state={deviceState} />
        ) : agentControllerRef.current ? (
          <AgentWorkspace controller={agentControllerRef.current} state={agentState} />
        ) : null}
      </WorkspaceShell>
    );
  }

  const terminalTitle = connection.state === "kicked" ? "该设备已退出" : connection.state === "expired" ? "登录已过期" : "OpenIM 不可用";
  const pageTitle = connection.state === "kicked" ? "远程注销" : connection.state === "expired" ? "会话失效" : "连接失败";
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
            <h1>{pageTitle}</h1>
          </div>
          <button className="icon-text-button" onClick={() => void logout()}><LogOut size={17} />退出</button>
        </header>

        <section className="status-band" aria-live="polite">
          <div className="status-icon danger"><AlertTriangle size={24} /></div>
          <div>
            <p className="status-label">实时通信</p>
            <h2>{terminalTitle}</h2>
            {error && <p className="error-text">{error}</p>}
          </div>
          {phase === "error" && connection.state !== "kicked" && <button className="secondary-button" onClick={() => void retry()}><RefreshCw size={17} />重试</button>}
        </section>

      </main>
    </div>
  );
}
