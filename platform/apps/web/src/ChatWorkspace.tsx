import { ArrowLeft, BellOff, ChevronUp, CircleAlert, Download, FileText, ImagePlus, LoaderCircle, LogOut, MessageSquarePlus, MoreHorizontal, Paperclip, Pin, Plus, RefreshCw, Search, Send, Trash2, UserMinus, UserPlus, Users, Wifi, WifiOff, X } from "lucide-react";
import { GroupMemberRole, MessageReceiveOptType, MessageStatus, MessageType, SessionType, type ConversationItem, type GroupMemberItem, type MessageItem } from "@openim/wasm-client-sdk";
import { useEffect, useRef, useState } from "react";

import { canInviteGroupMembers, canRemoveGroupMember, type ChatState, type ConversationController } from "./chat";
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
    if (message.contentType === MessageType.PictureMessage) return "[图片]";
    if (message.contentType === MessageType.FileMessage) return `[文件] ${message.fileElem?.fileName ?? ""}`.trim();
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
  const [showInviteGroup, setShowInviteGroup] = useState(false);
  const [inviteMemberIDs, setInviteMemberIDs] = useState<string[]>([]);
  const [pendingGroupAction, setPendingGroupAction] = useState<{ kind: "remove"; member: GroupMemberItem } | { kind: "leave" | "dismiss" } | null>(null);
  const [conversationMenuID, setConversationMenuID] = useState<string | null>(null);
  const [groupName, setGroupName] = useState("");
  const [groupMemberIDs, setGroupMemberIDs] = useState<string[]>([]);
  const [previewImage, setPreviewImage] = useState<{ url: string; alt: string } | null>(null);
  const messageEnd = useRef<HTMLDivElement | null>(null);
  const imageInput = useRef<HTMLInputElement | null>(null);
  const fileInput = useRef<HTMLInputElement | null>(null);
  const active = state.conversations.find((item) => item.conversationID === state.activeConversationID) ?? null;
  const visibleConversations = controller.visibleConversations();
  const selfGroupMember = state.groupMembers.find((member) => member.userID === selfUserID) ?? null;
  const canInvite = canInviteGroupMembers(state.groupMembers, selfUserID);

  useEffect(() => {
    messageEnd.current?.scrollIntoView({ block: "end" });
  }, [state.messages.length, state.activeConversationID]);

  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape") setConversationMenuID(null); };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, []);

  useEffect(() => {
    setShowInviteGroup(false);
    setInviteMemberIDs([]);
    setPendingGroupAction(null);
  }, [active?.groupID]);

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
    setConversationMenuID(null);
    try {
      await controller.select(conversationID);
      setMobileDetail(true);
      setShowGroupMembers(false);
    } catch {
      // The controller publishes the explicit error in ChatState.
    }
  };

  const updateConversationSetting = async (conversation: ConversationItem, action: "pin" | "mute") => {
    try {
      if (action === "pin") await controller.setPinned(conversation.conversationID, !conversation.isPinned);
      else await controller.setMuted(conversation.conversationID, conversation.recvMsgOpt !== MessageReceiveOptType.NotNotify);
      setConversationMenuID(null);
    } catch {
      // The controller publishes the explicit SDK or state error.
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

  const sendMedia = async (kind: "image" | "file", file: File) => {
    if (working || connection.state !== "connected") return;
    setWorking(true);
    try {
      if (kind === "image") await controller.sendImage(file);
      else await controller.sendFile(file);
    } catch {
      // The controller exposes validation, upload, and send failures in ChatState.
    } finally {
      setWorking(false);
    }
  };

  const inviteGroupMembers = async () => {
    try {
      await controller.inviteGroupMembers(inviteMemberIDs);
      setInviteMemberIDs([]);
      setShowInviteGroup(false);
    } catch {
      // The controller publishes the exact SDK or role error.
    }
  };

  const confirmGroupAction = async () => {
    if (!pendingGroupAction) return;
    try {
      if (pendingGroupAction.kind === "remove") await controller.removeGroupMember(pendingGroupAction.member.userID);
      else if (pendingGroupAction.kind === "leave") await controller.leaveActiveGroup();
      else await controller.dismissActiveGroup();
      setPendingGroupAction(null);
      if (pendingGroupAction.kind !== "remove") setShowGroupMembers(false);
    } catch {
      // The controller publishes the exact SDK or role error.
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

        <div className="conversation-search">
          <Search size={16} />
          <input
            aria-label="搜索会话"
            placeholder="搜索会话"
            value={state.conversationQuery}
            onChange={(event) => { controller.setConversationQuery(event.target.value); setConversationMenuID(null); }}
          />
          {state.conversationQuery && (
            <button type="button" aria-label="清除会话搜索" title="清除" onClick={() => controller.setConversationQuery("")}><X size={15} /></button>
          )}
        </div>

        <nav className="conversation-list">
          {visibleConversations.map((conversation) => (
            <div
              key={conversation.conversationID}
              className="conversation-item"
              data-testid={`conversation-item-${conversationSource(conversation)}`}
              data-pinned={String(conversation.isPinned)}
              data-muted={String(conversation.recvMsgOpt === MessageReceiveOptType.NotNotify)}
            >
              <button
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
                <span className="conversation-statuses">
                  {conversation.isPinned && <Pin size={13} aria-label="已置顶" />}
                  {conversation.recvMsgOpt === MessageReceiveOptType.NotNotify && <BellOff size={13} aria-label="已免打扰" />}
                  {conversation.recvMsgOpt === MessageReceiveOptType.NotReceive && <CircleAlert size={13} aria-label="不接收消息" />}
                  {conversation.unreadCount > 0 && <b className="unread-badge" data-testid={`unread-${conversation.userID}`}>{conversation.unreadCount}</b>}
                </span>
              </button>
              <button
                type="button"
                className="conversation-menu-trigger"
                aria-label={`会话操作 ${conversation.showName || conversationSource(conversation)}`}
                aria-expanded={conversationMenuID === conversation.conversationID}
                title="会话操作"
                onClick={() => setConversationMenuID((value) => value === conversation.conversationID ? null : conversation.conversationID)}
              >
                {state.conversationActionByID[conversation.conversationID] ? <LoaderCircle className="spin" size={16} /> : <MoreHorizontal size={17} />}
              </button>
              {conversationMenuID === conversation.conversationID && (
                <div className="conversation-menu" role="menu" aria-label={`管理会话 ${conversation.showName || conversationSource(conversation)}`}>
                  <button role="menuitem" disabled={Boolean(state.conversationActionByID[conversation.conversationID])} onClick={() => void updateConversationSetting(conversation, "pin")}>
                    <Pin size={15} />{conversation.isPinned ? "取消置顶" : "置顶会话"}
                  </button>
                  <button
                    role="menuitem"
                    disabled={Boolean(state.conversationActionByID[conversation.conversationID]) || conversation.recvMsgOpt === MessageReceiveOptType.NotReceive}
                    onClick={() => void updateConversationSetting(conversation, "mute")}
                  >
                    <BellOff size={15} />{conversation.recvMsgOpt === MessageReceiveOptType.NotReceive ? "当前不接收消息" : conversation.recvMsgOpt === MessageReceiveOptType.NotNotify ? "关闭免打扰" : "开启免打扰"}
                  </button>
                </div>
              )}
            </div>
          ))}
          {state.conversations.length === 0 && <p className="empty-note">暂无会话</p>}
          {state.conversations.length > 0 && visibleConversations.length === 0 && <p className="empty-note">没有匹配的会话</p>}
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
                const progress = state.uploadProgressByClientMsgID[message.clientMsgID];
                const pictureURL = message.contentType === MessageType.PictureMessage ? imageURL(message) : null;
                const fileURL = message.contentType === MessageType.FileMessage ? safeMediaURL(message.fileElem?.sourceUrl) : null;
                return (
                  <article key={message.clientMsgID} className={`message-row ${outgoing ? "outgoing" : "incoming"}`} data-message-id={message.clientMsgID}>
                    <div>
                      {!outgoing && active.conversationType === SessionType.Group && <div className="message-sender">{message.senderNickname || message.sendID}</div>}
                      {message.contentType === MessageType.PictureMessage && (
                        <div className="media-message image-message" data-testid={`image-message-${message.clientMsgID}`}>
                          {pictureURL ? (
                            <button type="button" className="image-preview-trigger" aria-label="预览图片" onClick={() => setPreviewImage({ url: pictureURL, alt: `来自 ${message.senderNickname || message.sendID} 的图片` })}>
                              <img src={pictureURL} alt="聊天图片" loading="lazy" />
                            </button>
                          ) : <div className="media-unavailable"><ImagePlus size={19} /><span>图片地址不可用</span></div>}
                        </div>
                      )}
                      {message.contentType === MessageType.FileMessage && (
                        <div className="media-message file-message" data-testid={`file-message-${message.clientMsgID}`}>
                          <span className="file-icon"><FileText size={22} /></span>
                          <span className="file-copy"><strong>{message.fileElem?.fileName || "未命名文件"}</strong><small>{formatBytes(message.fileElem?.fileSize ?? 0)}</small></span>
                          {fileURL ? (
                            <a href={fileURL} target="_blank" rel="noreferrer" download={message.fileElem?.fileName} aria-label={`下载文件 ${message.fileElem?.fileName || "未命名文件"}`} title="下载文件"><Download size={18} /></a>
                          ) : <span className="file-unavailable" title="文件地址不可用"><CircleAlert size={17} /></span>}
                        </div>
                      )}
                      {message.contentType === MessageType.TextMessage && <div className="message-bubble">{message.textElem?.content}</div>}
                      {progress !== undefined && message.status === MessageStatus.Sending && (
                        <div className="upload-progress" role="progressbar" aria-label={`上传进度 ${progress}%`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress}>
                          <span style={{ width: `${progress}%` }} />
                        </div>
                      )}
                    </div>
                    <div className="message-meta"><time>{messageTime(message)}</time>{outgoing && <span className={message.status === MessageStatus.Failed ? "failed" : ""}>{sendState(message)}</span>}</div>
                  </article>
                );
              })}
              {state.messages.length === 0 && !state.loadingHistory && <p className="empty-note centered">还没有消息</p>}
              <div ref={messageEnd} />
            </div>
            <footer className="composer">
              <div className="composer-main">
                <div className="composer-tools" aria-label="消息附件">
                  <button type="button" className="composer-tool" title="发送图片" aria-label="发送图片" disabled={working || connection.state !== "connected"} onClick={() => imageInput.current?.click()}><ImagePlus size={19} /></button>
                  <input ref={imageInput} className="visually-hidden" type="file" aria-label="选择图片" accept="image/jpeg,image/png,image/gif,image/webp" onChange={(event) => {
                    const file = event.currentTarget.files?.[0];
                    event.currentTarget.value = "";
                    if (file) void sendMedia("image", file);
                  }} />
                  <button type="button" className="composer-tool" title="发送文件" aria-label="发送文件" disabled={working || connection.state !== "connected"} onClick={() => fileInput.current?.click()}><Paperclip size={19} /></button>
                  <input ref={fileInput} className="visually-hidden" type="file" aria-label="选择文件" onChange={(event) => {
                    const file = event.currentTarget.files?.[0];
                    event.currentTarget.value = "";
                    if (file) void sendMedia("file", file);
                  }} />
                </div>
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
              </div>
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
                {canInvite && (
                  <button className="icon-button" aria-label="邀请群成员" title="邀请成员" disabled={Boolean(state.groupAction)} onClick={() => setShowInviteGroup(true)}>
                    <UserPlus size={17} />
                  </button>
                )}
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
                  <span><strong>{member.nickname || member.userID}</strong><small>{member.userID}{member.roleLevel === GroupMemberRole.Owner ? " · 群主" : member.roleLevel === GroupMemberRole.Admin ? " · 管理员" : ""}</small></span>
                  {canRemoveGroupMember(state.groupMembers, selfUserID, member.userID) && (
                    <button className="member-remove" aria-label={`移除群成员 ${member.nickname || member.userID}`} title="移除成员" disabled={Boolean(state.groupAction)} onClick={() => setPendingGroupAction({ kind: "remove", member })}>
                      <UserMinus size={16} />
                    </button>
                  )}
                </div>
              ))}
              {!state.loadingGroupMembers && state.groupMembers.length === 0 && <p className="empty-note">暂无成员数据</p>}
            </div>
            {selfGroupMember && (
              <footer className="group-member-footer">
                {selfGroupMember.roleLevel === GroupMemberRole.Owner ? (
                  <button className="group-danger-action" disabled={Boolean(state.groupAction)} onClick={() => setPendingGroupAction({ kind: "dismiss" })}><Trash2 size={16} />解散群聊</button>
                ) : (
                  <button className="group-danger-action" disabled={Boolean(state.groupAction)} onClick={() => setPendingGroupAction({ kind: "leave" })}><LogOut size={16} />退出群聊</button>
                )}
              </footer>
            )}
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

      {showInviteGroup && active?.conversationType === SessionType.Group && (
        <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !state.groupAction) setShowInviteGroup(false); }}>
          <section className="create-group-dialog group-lifecycle-dialog" role="dialog" aria-modal="true" aria-label="邀请群成员">
            <header>
              <div><span className="dialog-icon"><UserPlus size={19} /></span><h2>邀请群成员</h2></div>
              <button className="icon-button" aria-label="关闭邀请群成员" title="关闭" disabled={Boolean(state.groupAction)} onClick={() => setShowInviteGroup(false)}><X size={18} /></button>
            </header>
            <MemberPicker
              controller={contactController}
              state={contactState}
              selfUserID={selfUserID}
              selectedUserIDs={inviteMemberIDs}
              excludedUserIDs={state.groupMembers.map((member) => member.userID)}
              onChange={setInviteMemberIDs}
              disabled={Boolean(state.groupAction)}
            />
            {state.error && <div className="inline-error" role="alert"><span>{state.error}</span><button type="button" onClick={() => controller.clearError()}>关闭</button></div>}
            <footer>
              <button className="secondary-button" disabled={Boolean(state.groupAction)} onClick={() => setShowInviteGroup(false)}>取消</button>
              <button className="primary-button" disabled={Boolean(state.groupAction) || inviteMemberIDs.length === 0} onClick={() => void inviteGroupMembers()}>
                {state.groupAction?.kind === "invite" ? <LoaderCircle className="spin" size={17} /> : <UserPlus size={17} />}邀请
              </button>
            </footer>
          </section>
        </div>
      )}

      {pendingGroupAction && active?.conversationType === SessionType.Group && (
        <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !state.groupAction) setPendingGroupAction(null); }}>
          <section className="group-confirm-dialog" role="alertdialog" aria-modal="true" aria-label={pendingGroupAction.kind === "remove" ? "确认移除群成员" : pendingGroupAction.kind === "leave" ? "确认退出群聊" : "确认解散群聊"}>
            <span className="danger-dialog-icon">{pendingGroupAction.kind === "remove" ? <UserMinus size={20} /> : pendingGroupAction.kind === "leave" ? <LogOut size={20} /> : <Trash2 size={20} />}</span>
            <div>
              <h2>{pendingGroupAction.kind === "remove" ? "移除群成员" : pendingGroupAction.kind === "leave" ? "退出群聊" : "解散群聊"}</h2>
              <p>{pendingGroupAction.kind === "remove" ? `确认将 ${pendingGroupAction.member.nickname || pendingGroupAction.member.userID} 移出群聊？` : pendingGroupAction.kind === "leave" ? "退出后将不再接收该群消息。" : "解散后所有成员都将失去该群聊，操作不可撤销。"}</p>
            </div>
            {state.error && <div className="inline-error" role="alert"><span>{state.error}</span><button type="button" onClick={() => controller.clearError()}>关闭</button></div>}
            <footer>
              <button className="secondary-button" disabled={Boolean(state.groupAction)} onClick={() => setPendingGroupAction(null)}>取消</button>
              <button className="danger-button" disabled={Boolean(state.groupAction)} onClick={() => void confirmGroupAction()}>
                {state.groupAction ? <LoaderCircle className="spin" size={17} /> : null}确认
              </button>
            </footer>
          </section>
        </div>
      )}

      {previewImage && (
        <div className="image-preview-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setPreviewImage(null); }}>
          <section className="image-preview-dialog" role="dialog" aria-modal="true" aria-label="图片预览">
            <button type="button" className="preview-close" aria-label="关闭图片预览" title="关闭" onClick={() => setPreviewImage(null)}><X size={20} /></button>
            <img src={previewImage.url} alt={previewImage.alt} />
          </section>
        </div>
      )}
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB"];
  let value = bytes / 1024;
  let unit = units[0];
  for (let index = 1; index < units.length && value >= 1024; index += 1) {
    value /= 1024;
    unit = units[index];
  }
  return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)} ${unit}`;
}

function safeMediaURL(value: string | undefined, allowBlob = false): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    if (url.username || url.password) return null;
    if (url.protocol === "https:" || url.protocol === "http:" || (allowBlob && url.protocol === "blob:")) return url.href;
  } catch {
    return null;
  }
  return null;
}

function imageURL(message: MessageItem): string | null {
  return safeMediaURL(
    message.pictureElem?.bigPicture?.url || message.pictureElem?.sourcePicture?.url || message.pictureElem?.snapshotPicture?.url,
    message.status === MessageStatus.Sending || message.status === MessageStatus.Failed
  );
}
