import { ArrowLeft, ChevronUp, CircleAlert, LoaderCircle, MessageSquarePlus, Plus, RefreshCw, Send, Users, Wifi, WifiOff, X } from "lucide-react";
import { MessageStatus, SessionType, type ConversationItem, type MessageItem } from "@openim/wasm-client-sdk";
import { useEffect, useRef, useState } from "react";

import type { ChatState, ConversationController } from "./chat";
import type { ContactController, ContactState } from "./contact";
import { MemberPicker } from "./MemberPicker";
import type { ConnectionUpdate } from "./openim";

type ChatWorkspaceProps = {
  controller: ConversationController;
  state: ChatState;
  selfUserID: string;
  connection: ConnectionUpdate;
  contactController: ContactController;
  contactState: ContactState;
};

function conversationSource(conversation: ConversationItem): string {
  return conversation.conversationType === SessionType.Group ? conversation.groupID : conversation.userID;
}

function latestText(conversation: ConversationItem): string {
  if (!conversation.latestMsg) return "";
  try {
    const message = JSON.parse(conversation.latestMsg) as MessageItem;
    return message.textElem?.content ?? "";
  } catch {
    return "";
  }
}

function messageTime(message: MessageItem): string {
  const value = message.sendTime || message.createTime;
  return value ? new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";
}

function sendState(message: MessageItem): string {
  if (message.status === MessageStatus.Sending) return "发送中";
  if (message.status === MessageStatus.Failed) return "发送失败";
  return "已发送";
}

export function ChatWorkspace({ controller, state, selfUserID, connection, contactController, contactState }: ChatWorkspaceProps) {
  const [targetUserID, setTargetUserID] = useState("");
  const [draft, setDraft] = useState("");
  const [working, setWorking] = useState(false);
  const [mobileDetail, setMobileDetail] = useState(false);
  const [showCreateGroup, setShowCreateGroup] = useState(false);
  const [showGroupMembers, setShowGroupMembers] = useState(false);
  const [groupName, setGroupName] = useState("");
  const [groupMemberIDs, setGroupMemberIDs] = useState<string[]>([]);
  const messageEnd = useRef<HTMLDivElement | null>(null);
  const active = state.conversations.find((item) => item.conversationID === state.activeConversationID) ?? null;

  useEffect(() => {
    messageEnd.current?.scrollIntoView({ block: "end" });
  }, [state.messages.length, state.activeConversationID]);

  const openDirect = async () => {
    if (working) return;
    setWorking(true);
    try {
      await controller.openDirect(targetUserID);
      setTargetUserID("");
      setMobileDetail(true);
    } catch {
      // The controller publishes the explicit error in ChatState.
    } finally {
      setWorking(false);
    }
  };

  const selectConversation = async (conversationID: string) => {
    try {
      await controller.select(conversationID);
      setMobileDetail(true);
      setShowGroupMembers(false);
    } catch {
      // The controller publishes the explicit error in ChatState.
    }
  };

  const createGroup = async () => {
    if (working) return;
    setWorking(true);
    try {
      await controller.createGroup(groupName, groupMemberIDs);
      setGroupName("");
      setGroupMemberIDs([]);
      setShowCreateGroup(false);
      setMobileDetail(true);
    } catch {
      // The controller publishes the explicit error in ChatState.
    } finally {
      setWorking(false);
    }
  };

  const send = async () => {
    if (working || !draft.trim()) return;
    const content = draft;
    setDraft("");
    setWorking(true);
    try {
      await controller.sendText(content);
    } catch {
      // The failed optimistic message and error remain visible in ChatState.
    } finally {
      setWorking(false);
    }
  };

  return (
    <div className={`chat-layout ${mobileDetail && active ? "mobile-detail" : ""}`}>
      <aside className="conversation-pane" aria-label="会话">
        <header className="conversation-header">
          <div>
            <h1>消息</h1>
            <p>最近会话</p>
          </div>
          <div className="conversation-actions">
            <span className="unread-total" data-testid="total-unread" aria-label={`总未读 ${state.totalUnread}`}>{state.totalUnread}</span>
            <button className="icon-button" title="创建群聊" aria-label="创建群聊" onClick={() => setShowCreateGroup(true)}>
              <Plus size={18} />
            </button>
          </div>
        </header>

        <div className="direct-entry">
          <input
            aria-label="OpenIM 用户 ID"
            placeholder="OpenIM 用户 ID"
            value={targetUserID}
            onChange={(event) => setTargetUserID(event.target.value)}
            onKeyDown={(event) => { if (event.key === "Enter") void openDirect(); }}
          />
          <button className="icon-button" title="发起单聊" aria-label="发起单聊" disabled={!targetUserID.trim() || working} onClick={() => void openDirect()}>
            <MessageSquarePlus size={18} />
          </button>
        </div>

        <nav className="conversation-list">
          {state.conversations.map((conversation) => (
            <button
              key={conversation.conversationID}
              data-testid={`conversation-${conversationSource(conversation)}`}
              className={`conversation-row ${conversation.conversationID === state.activeConversationID ? "selected" : ""}`}
              onClick={() => void selectConversation(conversation.conversationID)}
            >
              <span className={`avatar ${conversation.conversationType === SessionType.Group ? "group-avatar" : ""}`}>
                {conversation.conversationType === SessionType.Group ? <Users size={18} /> : (conversation.showName || conversation.userID).slice(0, 1).toUpperCase()}
              </span>
              <span className="conversation-copy">
                <strong>{conversation.showName || conversationSource(conversation)}</strong>
                <small>{latestText(conversation)}</small>
              </span>
              {conversation.unreadCount > 0 && <b className="unread-badge" data-testid={`unread-${conversation.userID}`}>{conversation.unreadCount}</b>}
            </button>
          ))}
          {state.conversations.length === 0 && <p className="empty-note">暂无会话</p>}
        </nav>
      </aside>

      <section className="message-pane" aria-label={active?.conversationType === SessionType.Group ? "群聊消息" : "单聊消息"}>
        <header className="message-header">
          <button className="mobile-back" aria-label="返回会话列表" title="返回" onClick={() => setMobileDetail(false)}>
            <ArrowLeft size={20} />
          </button>
          <span className={`avatar active-avatar ${active?.conversationType === SessionType.Group ? "group-avatar" : ""}`}>
            {active?.conversationType === SessionType.Group ? <Users size={18} /> : active ? (active.showName || active.userID).slice(0, 1).toUpperCase() : "?"}
          </span>
          <div className="active-conversation">
            <h2>{active?.showName || (active ? conversationSource(active) : "选择会话")}</h2>
            <span data-testid={active?.conversationType === SessionType.Group ? "active-group-id" : active ? "active-peer-id" : "self-user-id"}>
              {active ? conversationSource(active) : selfUserID}
            </span>
          </div>
          {active?.conversationType === SessionType.Group && (
            <button className="icon-button member-toggle" aria-label="查看群成员" title="群成员" onClick={() => setShowGroupMembers((value) => !value)}>
              <Users size={18} />
            </button>
          )}
          <div className={`connection-pill ${connection.state}`} data-testid="connection-state">
            {connection.state === "connected" ? <Wifi size={15} /> : connection.state === "connecting" ? <LoaderCircle className="spin" size={15} /> : <WifiOff size={15} />}
            {state.restoring ? "正在恢复" : connection.state === "connected" ? "在线" : connection.state === "connecting" ? "重连中" : "连接异常"}
          </div>
        </header>

        {state.error && (
          <div className="chat-error" role="alert"><CircleAlert size={16} /><span>{state.error}</span><button onClick={() => controller.clearError()}>关闭</button></div>
        )}

        {active ? (
          <>
            <div className="message-scroll">
              {!state.historyEnded && (
                <button className="history-button" disabled={state.loadingHistory} onClick={() => void controller.loadOlder().catch(() => undefined)}>
                  <ChevronUp size={15} />{state.loadingHistory ? "加载中" : "加载更早"}
                </button>
              )}
              {state.messages.map((message) => {
                const outgoing = message.sendID === selfUserID;
                return (
                  <article key={message.clientMsgID} className={`message-row ${outgoing ? "outgoing" : "incoming"}`} data-message-id={message.clientMsgID}>
                    <div>
                      {!outgoing && active.conversationType === SessionType.Group && <div className="message-sender">{message.senderNickname || message.sendID}</div>}
                      <div className="message-bubble">{message.textElem?.content}</div>
                    </div>
                    <div className="message-meta"><time>{messageTime(message)}</time>{outgoing && <span className={message.status === MessageStatus.Failed ? "failed" : ""}>{sendState(message)}</span>}</div>
                  </article>
                );
              })}
              {state.messages.length === 0 && !state.loadingHistory && <p className="empty-note centered">还没有文本消息</p>}
              <div ref={messageEnd} />
            </div>
            <footer className="composer">
              <textarea
                aria-label="消息内容"
                placeholder="输入消息"
                maxLength={6000}
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !event.shiftKey) {
                    event.preventDefault();
                    void send();
                  }
                }}
              />
              <button className="send-button" disabled={!draft.trim() || working || connection.state !== "connected"} onClick={() => void send()} title="发送" aria-label="发送">
                <Send size={18} />
              </button>
            </footer>
          </>
        ) : (
          <div className="conversation-empty"><MessageSquarePlus size={28} /><span>选择会话或输入用户 ID</span></div>
        )}

        {active?.conversationType === SessionType.Group && showGroupMembers && (
          <aside className="group-member-panel" aria-label="群成员">
            <header>
              <div><strong>群成员</strong><span>{state.groupMembers.length}</span></div>
              <div className="panel-actions">
                <button className="icon-button" aria-label="刷新群成员" title="刷新" disabled={state.loadingGroupMembers} onClick={() => void controller.refreshGroupMembers().catch(() => undefined)}>
                  <RefreshCw className={state.loadingGroupMembers ? "spin" : ""} size={17} />
                </button>
                <button className="icon-button" aria-label="关闭群成员" title="关闭" onClick={() => setShowGroupMembers(false)}><X size={18} /></button>
              </div>
            </header>
            <div className="group-member-list">
              {state.groupMembers.map((member) => (
                <div className="group-member-row" key={member.userID}>
                  <span className="avatar">{(member.nickname || member.userID).slice(0, 1).toUpperCase()}</span>
                  <span><strong>{member.nickname || member.userID}</strong><small>{member.userID}</small></span>
                </div>
              ))}
              {!state.loadingGroupMembers && state.groupMembers.length === 0 && <p className="empty-note">暂无成员数据</p>}
            </div>
          </aside>
        )}
      </section>

      {showCreateGroup && (
        <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setShowCreateGroup(false); }}>
          <section className="create-group-dialog" role="dialog" aria-modal="true" aria-labelledby="create-group-title">
            <header>
              <div><span className="dialog-icon"><Users size={19} /></span><h2 id="create-group-title">创建群聊</h2></div>
              <button className="icon-button" aria-label="关闭创建群聊" title="关闭" onClick={() => setShowCreateGroup(false)}><X size={18} /></button>
            </header>
            <label>群名称<input maxLength={60} value={groupName} onChange={(event) => setGroupName(event.target.value)} placeholder="例如：项目讨论组" /></label>
            <MemberPicker controller={contactController} state={contactState} selfUserID={selfUserID} selectedUserIDs={groupMemberIDs} onChange={setGroupMemberIDs} disabled={working} />
            {state.error && <div className="inline-error" role="alert"><span>{state.error}</span><button type="button" onClick={() => controller.clearError()}>关闭</button></div>}
            <footer>
              <button className="secondary-button" onClick={() => setShowCreateGroup(false)}>取消</button>
              <button className="primary-button" disabled={working || !groupName.trim() || groupMemberIDs.length === 0} onClick={() => void createGroup()}>
                {working ? <LoaderCircle className="spin" size={17} /> : <Users size={17} />}创建
              </button>
            </footer>
          </section>
        </div>
      )}
    </div>
  );
}
