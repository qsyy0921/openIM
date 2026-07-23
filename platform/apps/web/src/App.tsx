import { AlertTriangle, BookOpen, Bot, ContactRound, Link2, LogIn, LogOut, MessageCircle, MessageSquare, MonitorSmartphone, RefreshCw, Wifi } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { User, UserManager } from "oidc-client-ts";
import { ApplicationHandleResult, SessionType } from "@openim/wasm-client-sdk";

import type { WebConfig } from "./config";
import { AgentWorkspace } from "./AgentWorkspace";
import { AgentControlController, initialAgentControlState, type AgentControlState } from "./agent-control";
import {
  acknowledgeAgentEvent,
  createCatalogAgent,
  createAgentSubscription,
  decideAgentToolApproval,
  deleteAgentMemoryFact,
  feedbackAgentMemory,
  getAgentMemory,
  getAgentGroupMemory,
  getAgentAdminSnapshot,
  getAgentAdminCatalog,
  getAgentDelegations,
  getAgentProactive,
  getAgentReplay,
  getAgentToolApprovals,
  setAgentSubscriptionEnabled,
  setAgentRuntimeControl,
  setCatalogMCPEnabled,
  setCatalogMemberRole,
  publishCapabilitySnapshot,
  publishCatalogSkill,
  publishCatalogTool,
  registerRemoteA2AAgent,
  verifyRemoteA2AAgent,
  setRemoteA2AAgentEnabled,
  reviewAgentGroupMemory,
  updateAgentProactivePreference
} from "./agent-control-api";
import { AgentController, createOpenIMAgentTransport, initialAgentState, type AgentState } from "./agent";
import { approveAgentIntent, getAgentCatalog, getAgentWorkspace } from "./agent-api";
import { ChatWorkspace } from "./ChatWorkspace";
import { ChannelWorkspace } from "./ChannelWorkspace";
import { ConversationController, createOpenIMChatPort, initialChatState, type ChatState } from "./chat";
import { ContactController, createOpenIMContactPort, initialContactState, type ContactState } from "./contact";
import { ContactsWorkspace } from "./ContactsWorkspace";
import { DeviceWorkspace } from "./DeviceWorkspace";
import { DeviceController, initialDeviceState, type DeviceState } from "./device";
import { getDevices, logoutPlatform } from "./device-api";
import {
  admitCallbackIdentity,
  EnterpriseSessionController,
  EnterpriseSessionError,
  restoreEnterpriseIdentity,
  type EnterpriseSessionFailure
} from "./enterprise-session";
import { createOpenIMMessageSearchPort, initialMessageSearchState, MessageSearchController, type MessageSearchState } from "./message-search";
import { connectOpenIM, disconnectOpenIM, type ConnectionUpdate } from "./openim";
import { createIMSession, type IMSession } from "./platform-api";
import { getTelegramLinkStatus, issueTelegramLinkChallenge } from "./telegram-link-api";
import { initialTelegramLinkState, TelegramLinkController, type TelegramLinkState } from "./telegram-link";
import {
  getKnowledgeSnapshot,
  listKnowledgeVersions,
  publishKnowledgeVersion,
  setKnowledgeGrant,
  unpublishKnowledge,
  uploadKnowledge
} from "./knowledge-api";
import { initialKnowledgeState, KnowledgeController, type KnowledgeState } from "./knowledge";
import { KnowledgeWorkspace } from "./KnowledgeWorkspace";
import { WorkspaceShell, type WorkspaceModule } from "./WorkspaceShell";

type Phase = "booting" | "signed-out" | "exchanging" | "connecting" | "connected" | "error";

type AppProps = {
  config: WebConfig;
  userManager: UserManager;
};

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : "unexpected client failure";
}

function enterpriseSessionMessage(reason: EnterpriseSessionFailure): string {
  if (reason === "identity-changed") return "企业身份发生变化，请重新登录";
  if (reason === "session-clear-failed") return "企业会话失效且本地状态未能清除，请关闭此页面后重新登录";
  return "企业登录已失效，请重新登录";
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
  const [telegramLinkState, setTelegramLinkState] = useState<TelegramLinkState>(initialTelegramLinkState);
  const [agentState, setAgentState] = useState<AgentState>(initialAgentState);
  const [agentControlState, setAgentControlState] = useState<AgentControlState>(initialAgentControlState);
  const [knowledgeState, setKnowledgeState] = useState<KnowledgeState>(initialKnowledgeState);
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
  const telegramLinkControllerRef = useRef<TelegramLinkController | null>(null);
  const unsubscribeTelegramLinkRef = useRef<(() => void) | null>(null);
  const telegramLinkStartedRef = useRef(false);
  const agentControllerRef = useRef<AgentController | null>(null);
  const unsubscribeAgentRef = useRef<(() => void) | null>(null);
  const agentControlControllerRef = useRef<AgentControlController | null>(null);
  const unsubscribeAgentControlRef = useRef<(() => void) | null>(null);
  const knowledgeControllerRef = useRef<KnowledgeController | null>(null);
  const unsubscribeKnowledgeRef = useRef<(() => void) | null>(null);
  const agentStartedRef = useRef(false);
  const enterpriseSessionRef = useRef<EnterpriseSessionController | null>(null);
  const connectAttemptRef = useRef(0);
  const openIMConnectedRef = useRef(false);

  const currentIDToken = useCallback(() => {
    const enterpriseSession = enterpriseSessionRef.current;
    if (!enterpriseSession) throw new EnterpriseSessionError("identity-unloaded");
    return enterpriseSession.idToken();
  }, []);

  const closeWorkspaceRuntime = useCallback(async (resetPresentation = true): Promise<string | null> => {
    connectAttemptRef.current += 1;
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
    telegramLinkControllerRef.current?.close();
    telegramLinkControllerRef.current = null;
    unsubscribeTelegramLinkRef.current?.();
    unsubscribeTelegramLinkRef.current = null;
    telegramLinkStartedRef.current = false;
    agentControllerRef.current?.stop();
    agentControllerRef.current = null;
    unsubscribeAgentRef.current?.();
    unsubscribeAgentRef.current = null;
    agentControlControllerRef.current?.stop();
    agentControlControllerRef.current = null;
    unsubscribeAgentControlRef.current?.();
    unsubscribeAgentControlRef.current = null;
    knowledgeControllerRef.current?.stop();
    knowledgeControllerRef.current = null;
    unsubscribeKnowledgeRef.current?.();
    unsubscribeKnowledgeRef.current = null;
    agentStartedRef.current = false;
    if (resetPresentation) {
      setChatState(initialChatState);
      setContactState(initialContactState);
      setMessageSearchState(initialMessageSearchState);
      setDeviceState(initialDeviceState);
      setTelegramLinkState(initialTelegramLinkState);
      setAgentState(initialAgentState);
      setAgentControlState(initialAgentControlState);
      setKnowledgeState(initialKnowledgeState);
      setActiveModule("messages");
      setSession(null);
    }
    if (!openIMConnectedRef.current) return null;
    openIMConnectedRef.current = false;
    try {
      await disconnectOpenIM();
      return null;
    } catch {
      return "OpenIM 会话未能确认断开";
    }
  }, []);

  const connect = useCallback(
    async () => {
      const attempt = ++connectAttemptRef.current;
      setError(null);
      setPhase("exchanging");
      try {
        const nextSession = await createIMSession(config.platformAPIBaseURL, currentIDToken(), config.deviceID);
        if (attempt !== connectAttemptRef.current) return;
        setSession(nextSession);
        setPhase("connecting");
        detachRef.current?.();
        const detach = await connectOpenIM(config, nextSession, (update: ConnectionUpdate) => {
          if (attempt !== connectAttemptRef.current) return;
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
              void Promise.all([
                agentControllerRef.current?.start(),
                agentControlControllerRef.current?.start()
              ]).catch((cause) => setError(messageOf(cause)));
            }
            return;
          }
          if (update.state === "connecting") {
            if (!chatControllerRef.current) setPhase("connecting");
            return;
          }
          if (update.state === "kicked" || update.state === "expired") {
            setError(update.message ?? update.state);
            setPhase("error");
            void closeWorkspaceRuntime().then((cleanupError) => {
              if (cleanupError) setError(`${update.message ?? update.state}; ${cleanupError}`);
            });
            return;
          }
          if (!chatControllerRef.current) {
            setError(update.message ?? update.state);
            setPhase("error");
          }
        });
        if (attempt !== connectAttemptRef.current) {
          detach();
          try {
            await disconnectOpenIM();
          } catch {
            // The terminal path has already closed the workspace.
          }
          return;
        }
        detachRef.current = detach;
        openIMConnectedRef.current = true;
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
          list: () => getDevices(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          logout: (platformID) => logoutPlatform(config.platformAPIBaseURL, currentIDToken(), config.deviceID, platformID)
        });
        deviceControllerRef.current = deviceController;
        unsubscribeDeviceRef.current = deviceController.subscribe(setDeviceState);
        deviceStartedRef.current = false;
        telegramLinkControllerRef.current?.close();
        unsubscribeTelegramLinkRef.current?.();
        const telegramLinkController = new TelegramLinkController({
          status: () => getTelegramLinkStatus(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          issue: () => issueTelegramLinkChallenge(config.platformAPIBaseURL, currentIDToken(), config.deviceID)
        });
        telegramLinkControllerRef.current = telegramLinkController;
        unsubscribeTelegramLinkRef.current = telegramLinkController.subscribe(setTelegramLinkState);
        telegramLinkStartedRef.current = false;
        agentControllerRef.current?.stop();
        unsubscribeAgentRef.current?.();
        const agentController = new AgentController({
          catalog: () => getAgentCatalog(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          workspace: () => getAgentWorkspace(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          approve: (intentID, digest) => approveAgentIntent(config.platformAPIBaseURL, currentIDToken(), config.deviceID, intentID, digest)
        }, createOpenIMAgentTransport());
        agentControllerRef.current = agentController;
        unsubscribeAgentRef.current = agentController.subscribe(setAgentState);
        agentControlControllerRef.current?.stop();
        unsubscribeAgentControlRef.current?.();
        const agentControlController = new AgentControlController({
          memory: () => getAgentMemory(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          groupMemory: (conversationID) => getAgentGroupMemory(config.platformAPIBaseURL, currentIDToken(), config.deviceID, conversationID),
          reviewGroupMemory: (conversationID, proposalID, decision) => reviewAgentGroupMemory(config.platformAPIBaseURL, currentIDToken(), config.deviceID, conversationID, proposalID, decision),
          deleteMemory: (factID, key) => deleteAgentMemoryFact(config.platformAPIBaseURL, currentIDToken(), config.deviceID, factID, key),
          feedbackMemory: (exposureID, signal) => feedbackAgentMemory(config.platformAPIBaseURL, currentIDToken(), config.deviceID, exposureID, signal),
          proactive: () => getAgentProactive(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          createSubscription: (input) => createAgentSubscription(config.platformAPIBaseURL, currentIDToken(), config.deviceID, input),
          setSubscriptionEnabled: (subscriptionID, enabled) => setAgentSubscriptionEnabled(config.platformAPIBaseURL, currentIDToken(), config.deviceID, subscriptionID, enabled),
          updatePreference: (preference) => updateAgentProactivePreference(config.platformAPIBaseURL, currentIDToken(), config.deviceID, preference),
          acknowledge: (eventID, signal) => acknowledgeAgentEvent(config.platformAPIBaseURL, currentIDToken(), config.deviceID, eventID, signal),
          toolApprovals: () => getAgentToolApprovals(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          decideToolApproval: (approvalID, digest, decision) => decideAgentToolApproval(config.platformAPIBaseURL, currentIDToken(), config.deviceID, approvalID, digest, decision),
          replay: (runID) => getAgentReplay(config.platformAPIBaseURL, currentIDToken(), config.deviceID, runID),
          delegations: () => getAgentDelegations(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          adminOperations: () => getAgentAdminSnapshot(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          adminCatalog: () => getAgentAdminCatalog(config.platformAPIBaseURL, currentIDToken(), config.deviceID),
          setRuntimeControl: (control, paused, reason) => setAgentRuntimeControl(config.platformAPIBaseURL, currentIDToken(), config.deviceID, control, paused, reason),
          createAgent: (input) => createCatalogAgent(config.platformAPIBaseURL, currentIDToken(), config.deviceID, input),
          publishSkill: (input) => publishCatalogSkill(config.platformAPIBaseURL, currentIDToken(), config.deviceID, input),
          publishTool: (input) => publishCatalogTool(config.platformAPIBaseURL, currentIDToken(), config.deviceID, input),
          publishCapabilitySnapshot: (tools) => publishCapabilitySnapshot(config.platformAPIBaseURL, currentIDToken(), config.deviceID, tools),
          setMCPEnabled: (slug, enabled) => setCatalogMCPEnabled(config.platformAPIBaseURL, currentIDToken(), config.deviceID, slug, enabled),
          setMemberRole: (memberID, role, enabled) => setCatalogMemberRole(config.platformAPIBaseURL, currentIDToken(), config.deviceID, memberID, role, enabled),
          registerRemoteAgent: (input) => registerRemoteA2AAgent(config.platformAPIBaseURL, currentIDToken(), config.deviceID, input),
          verifyRemoteAgent: (slug, expectedRevision) => verifyRemoteA2AAgent(config.platformAPIBaseURL, currentIDToken(), config.deviceID, slug, expectedRevision),
          setRemoteAgentEnabled: (slug, enabled, expectedRevision) => setRemoteA2AAgentEnabled(config.platformAPIBaseURL, currentIDToken(), config.deviceID, slug, enabled, expectedRevision)
        });
        agentControlControllerRef.current = agentControlController;
        unsubscribeAgentControlRef.current = agentControlController.subscribe(setAgentControlState);
        knowledgeControllerRef.current?.stop();
        unsubscribeKnowledgeRef.current?.();
        const knowledgeController = new KnowledgeController({
          snapshot: (documentID) => getKnowledgeSnapshot(config.platformAPIBaseURL, currentIDToken(), config.deviceID, documentID),
          versions: (documentID) => listKnowledgeVersions(config.platformAPIBaseURL, currentIDToken(), config.deviceID, documentID),
          upload: (input) => uploadKnowledge(config.platformAPIBaseURL, currentIDToken(), config.deviceID, input),
          publish: (documentID, versionID) => publishKnowledgeVersion(config.platformAPIBaseURL, currentIDToken(), config.deviceID, documentID, versionID),
          unpublish: (documentID) => unpublishKnowledge(config.platformAPIBaseURL, currentIDToken(), config.deviceID, documentID),
          setGrant: (documentID, memberID, enabled) => setKnowledgeGrant(config.platformAPIBaseURL, currentIDToken(), config.deviceID, documentID, memberID, enabled)
        });
        knowledgeControllerRef.current = knowledgeController;
        unsubscribeKnowledgeRef.current = knowledgeController.subscribe(setKnowledgeState);
        void knowledgeController.start();
        agentStartedRef.current = false;
        setConnection({ state: "connected" });
        setPhase("connected");
      } catch (cause) {
        if (attempt !== connectAttemptRef.current) return;
        setError(messageOf(cause));
        setPhase("error");
      }
    },
    [closeWorkspaceRuntime, config, currentIDToken]
  );

  useEffect(() => {
    let active = true;
    let lifecycle: EnterpriseSessionController | null = null;
    const initialize = async () => {
      try {
        const callback = window.location.pathname === new URL(config.oidcRedirectURI).pathname;
        let identity: User | null;
        if (callback) {
          const callbackIdentity = await userManager.signinRedirectCallback();
          identity = await admitCallbackIdentity(userManager, callbackIdentity);
        } else {
          identity = await restoreEnterpriseIdentity(userManager);
        }
        if (callback) {
          window.history.replaceState({}, document.title, "/");
        }
        if (!active) return;
        if (!identity) {
          setUser(null);
          setPhase("signed-out");
          return;
        }
        lifecycle = new EnterpriseSessionController(userManager, identity, {
          onUserChanged: (nextIdentity) => {
            if (!active || enterpriseSessionRef.current !== lifecycle) return;
            setUser(nextIdentity);
          },
          onTerminal: async (reason) => {
            if (!active || enterpriseSessionRef.current !== lifecycle) return;
            enterpriseSessionRef.current = null;
            const cleanupError = await closeWorkspaceRuntime();
            if (!active) return;
            setUser(null);
            setConnection({ state: "expired", message: "Enterprise identity unavailable" });
            setError(cleanupError ? `${enterpriseSessionMessage(reason)}；${cleanupError}` : enterpriseSessionMessage(reason));
            setPhase("signed-out");
          }
        });
        enterpriseSessionRef.current = lifecycle;
        lifecycle.start();
        setUser(identity);
        await connect();
      } catch (cause) {
        if (!active) return;
        lifecycle?.close();
        if (enterpriseSessionRef.current === lifecycle) enterpriseSessionRef.current = null;
        setUser(null);
        setError(cause instanceof EnterpriseSessionError ? enterpriseSessionMessage(cause.code) : "企业登录失败，请重新登录");
        setPhase("signed-out");
      }
    };
    void initialize();
    return () => {
      active = false;
      lifecycle?.close();
      if (enterpriseSessionRef.current === lifecycle) enterpriseSessionRef.current = null;
      void closeWorkspaceRuntime(false);
    };
  }, [closeWorkspaceRuntime, config.oidcRedirectURI, connect, userManager]);

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
      enterpriseSessionRef.current?.close();
      enterpriseSessionRef.current = null;
      const cleanupError = await closeWorkspaceRuntime();
      if (cleanupError) throw new Error(cleanupError);
      setUser(null);
      await userManager.signoutRedirect();
    } catch (cause) {
      setError(messageOf(cause));
      setPhase("error");
    }
  };

  const retry = async () => {
    if (!user || !enterpriseSessionRef.current) {
      setPhase("signed-out");
      return;
    }
    await connect();
  };

  const displayName = useMemo(() => {
    const profile = user?.profile;
    return String(profile?.name ?? profile?.preferred_username ?? profile?.sub ?? "Enterprise member");
  }, [user]);

  const workspaceModules = useMemo<WorkspaceModule[]>(() => {
    const pendingContacts = contactState.incomingApplications.filter((application) => application.handleResult === ApplicationHandleResult.Unprocessed).length;
    const modules: WorkspaceModule[] = [
      { id: "messages", label: "消息", icon: MessageCircle },
      { id: "contacts", label: "通讯录", icon: ContactRound, badge: pendingContacts },
      { id: "devices", label: "设备", icon: MonitorSmartphone },
      { id: "channels", label: "渠道", icon: Link2 },
      { id: "agent", label: "智能助手", icon: Bot }
    ];
    if (knowledgeState.visibility === "visible") {
      modules.splice(4, 0, { id: "knowledge", label: "知识库", icon: BookOpen });
    }
    return modules;
  }, [contactState.incomingApplications, knowledgeState.visibility]);

  useEffect(() => {
    if (activeModule === "knowledge" && knowledgeState.visibility !== "visible") {
      setActiveModule("messages");
    }
  }, [activeModule, knowledgeState.visibility]);

  const selectModule = async (moduleID: string) => {
    if (moduleID !== "messages" && moduleID !== "contacts" && moduleID !== "devices" && moduleID !== "channels" && moduleID !== "knowledge" && moduleID !== "agent") return;
    if (moduleID === "knowledge" && knowledgeState.visibility !== "visible") return;
    setActiveModule(moduleID);
    if (moduleID === "devices" && !deviceStartedRef.current) {
      deviceStartedRef.current = true;
      try {
        await deviceControllerRef.current?.start();
      } catch {
        // DeviceState contains the explicit initialization error.
      }
    }
    if (moduleID === "channels" && !telegramLinkStartedRef.current) {
      telegramLinkStartedRef.current = true;
      try {
        await telegramLinkControllerRef.current?.start();
      } catch {
        // TelegramLinkState contains the explicit initialization error.
      }
    }
    if (moduleID === "agent" && !agentStartedRef.current) {
      agentStartedRef.current = true;
      try {
        await Promise.all([agentControllerRef.current?.start(), agentControlControllerRef.current?.start()]);
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
          {error && <p className="error-text" role="alert">{error}</p>}
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

  if (phase === "connected" && session && chatControllerRef.current && contactControllerRef.current && messageSearchControllerRef.current && deviceControllerRef.current && telegramLinkControllerRef.current) {
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
        ) : activeModule === "channels" ? (
          <ChannelWorkspace controller={telegramLinkControllerRef.current} state={telegramLinkState} />
        ) : activeModule === "knowledge" && knowledgeControllerRef.current ? (
          <KnowledgeWorkspace
            controller={knowledgeControllerRef.current}
            state={knowledgeState}
            onTestQuestion={async (question) => {
              if (!agentStartedRef.current) {
                agentStartedRef.current = true;
                try {
                  await Promise.all([agentControllerRef.current?.start(), agentControlControllerRef.current?.start()]);
                } catch (error) {
                  agentStartedRef.current = false;
                  throw error;
                }
              }
              if (!agentControllerRef.current) throw new Error("智能助手尚未就绪");
              await agentControllerRef.current.sendPrompt(question);
              setActiveModule("agent");
            }}
          />
        ) : agentControllerRef.current && agentControlControllerRef.current ? (
          <AgentWorkspace controller={agentControllerRef.current} state={agentState} controlController={agentControlControllerRef.current} controlState={agentControlState} groups={chatState.conversations.filter((item) => item.conversationType === SessionType.Group && item.groupID).map((item) => ({ conversationID: item.conversationID, name: item.showName || item.groupID }))} />
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
