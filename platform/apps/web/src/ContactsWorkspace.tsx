import { ApplicationHandleResult, type FriendApplicationItem, type FriendUserItem } from "@openim/wasm-client-sdk";
import { Check, CircleAlert, Clock3, LoaderCircle, MessageCircle, RefreshCw, Search, UserPlus, Users, X } from "lucide-react";
import { useMemo, useState } from "react";

import type { ContactController, ContactState } from "./contact";

type ContactView = "friends" | "incoming" | "outgoing" | "find";

type ContactsWorkspaceProps = {
  controller: ContactController;
  state: ContactState;
  onOpenChat: (userID: string) => Promise<void>;
};

function applicationStatus(application: FriendApplicationItem): string {
  if (application.handleResult === ApplicationHandleResult.Agree) return "已同意";
  if (application.handleResult === ApplicationHandleResult.Reject) return "已拒绝";
  return "待处理";
}

function friendName(friend: FriendUserItem): string {
  return friend.remark || friend.nickname || friend.userID;
}

export function ContactsWorkspace({ controller, state, onOpenChat }: ContactsWorkspaceProps) {
  const [view, setView] = useState<ContactView>("friends");
  const [query, setQuery] = useState("");
  const [requestMessage, setRequestMessage] = useState("你好，我想添加你为好友");
  const [localError, setLocalError] = useState<string | null>(null);
  const pendingIncoming = useMemo(
    () => state.incomingApplications.filter((application) => application.handleResult === ApplicationHandleResult.Unprocessed),
    [state.incomingApplications]
  );
  const lookup = state.lookupResult;
  const lookupIsFriend = Boolean(lookup && state.friends.some((friend) => friend.userID === lookup.userID));
  const lookupHasPendingOutgoing = Boolean(lookup && state.outgoingApplications.some((application) => application.toUserID === lookup.userID && application.handleResult === ApplicationHandleResult.Unprocessed));

  const search = async () => {
    setLocalError(null);
    try {
      await controller.lookup(query);
    } catch {
      // ContactState exposes the exact lookup failure.
    }
  };

  const openChat = async (userID: string) => {
    setLocalError(null);
    try {
      await onOpenChat(userID);
    } catch (error) {
      setLocalError(error instanceof Error ? error.message : "无法打开单聊");
    }
  };

  const mutate = async (operation: () => Promise<void>) => {
    setLocalError(null);
    try {
      await operation();
    } catch {
      // ContactState exposes the authoritative SDK mutation failure.
    }
  };

  return (
    <div className="contacts-layout">
      <aside className="contacts-sidebar">
        <header className="contacts-header">
          <div><h1>通讯录</h1><p>OpenIM 好友与申请</p></div>
          <button className="subtle-icon-button" title="刷新通讯录" aria-label="刷新通讯录" disabled={state.refreshing} onClick={() => void controller.refresh().catch(() => undefined)}>
            <RefreshCw className={state.refreshing ? "spin" : ""} size={18} />
          </button>
        </header>
        <nav className="contact-view-nav" aria-label="通讯录分类">
          <button className={view === "friends" ? "active" : ""} onClick={() => setView("friends")}><Users size={17} /><span>好友</span><b>{state.friends.length}</b></button>
          <button className={view === "incoming" ? "active" : ""} onClick={() => setView("incoming")}><UserPlus size={17} /><span>收到的申请</span>{pendingIncoming.length > 0 && <b className="danger-count">{pendingIncoming.length}</b>}</button>
          <button className={view === "outgoing" ? "active" : ""} onClick={() => setView("outgoing")}><Clock3 size={17} /><span>发出的申请</span><b>{state.outgoingApplications.length}</b></button>
          <button className={view === "find" ? "active" : ""} onClick={() => setView("find")}><Search size={17} /><span>查找用户</span></button>
        </nav>
      </aside>

      <section className="contacts-main" aria-label="通讯录内容">
        <header className="contacts-main-header">
          <div>
            <h2>{view === "friends" ? "好友" : view === "incoming" ? "收到的申请" : view === "outgoing" ? "发出的申请" : "查找 OpenIM 用户"}</h2>
            <span>{view === "find" ? "当前支持按准确用户 ID 查找" : "关系数据由 OpenIM 同步"}</span>
          </div>
        </header>

        {(state.error || localError) && (
          <div className="contacts-error" role="alert"><CircleAlert size={17} /><span>{localError || state.error}</span><button onClick={() => { setLocalError(null); controller.clearError(); }}>关闭</button></div>
        )}

        <div className="contacts-scroll">
          {state.loading ? (
            <div className="contacts-loading"><LoaderCircle className="spin" size={22} /><span>正在同步通讯录</span></div>
          ) : view === "friends" ? (
            <div className="contact-list">
              {state.friends.map((friend) => (
                <article className="contact-row" key={friend.userID} data-testid={`friend-${friend.userID}`}>
                  <span className="avatar">{friendName(friend).slice(0, 1).toUpperCase()}</span>
                  <span className="contact-copy"><strong>{friendName(friend)}</strong><small>{friend.userID}</small></span>
                  <button className="secondary-button compact" onClick={() => void openChat(friend.userID)}><MessageCircle size={16} />发消息</button>
                </article>
              ))}
              {state.friends.length === 0 && <p className="empty-note">暂无好友</p>}
            </div>
          ) : view === "incoming" ? (
            <div className="contact-list">
              {state.incomingApplications.map((application) => {
                const pending = application.handleResult === ApplicationHandleResult.Unprocessed;
                const accepting = state.pendingOperations.includes(`accept:${application.fromUserID}`);
                const rejecting = state.pendingOperations.includes(`reject:${application.fromUserID}`);
                return (
                  <article className="contact-row application-row" key={`${application.fromUserID}-${application.toUserID}`} data-testid={`incoming-application-${application.fromUserID}`}>
                    <span className="avatar">{(application.fromNickname || application.fromUserID).slice(0, 1).toUpperCase()}</span>
                    <span className="contact-copy"><strong>{application.fromNickname || application.fromUserID}</strong><small>{application.reqMsg || "请求添加你为好友"}</small></span>
                    {pending ? <span className="application-actions">
                      <button className="secondary-button compact reject" disabled={accepting || rejecting} onClick={() => void mutate(() => controller.rejectApplication(application.fromUserID))}>{rejecting ? <LoaderCircle className="spin" size={15} /> : <X size={15} />}拒绝</button>
                      <button className="primary-button compact" disabled={accepting || rejecting} onClick={() => void mutate(() => controller.acceptApplication(application.fromUserID))}>{accepting ? <LoaderCircle className="spin" size={15} /> : <Check size={15} />}同意</button>
                    </span> : <span className="application-status">{applicationStatus(application)}</span>}
                  </article>
                );
              })}
              {state.incomingApplications.length === 0 && <p className="empty-note">暂无收到的好友申请</p>}
            </div>
          ) : view === "outgoing" ? (
            <div className="contact-list">
              {state.outgoingApplications.map((application) => (
                <article className="contact-row application-row" key={`${application.fromUserID}-${application.toUserID}`} data-testid={`outgoing-application-${application.toUserID}`}>
                  <span className="avatar">{(application.toNickname || application.toUserID).slice(0, 1).toUpperCase()}</span>
                  <span className="contact-copy"><strong>{application.toNickname || application.toUserID}</strong><small>{application.reqMsg || "好友申请"}</small></span>
                  <span className="application-status">{applicationStatus(application)}</span>
                </article>
              ))}
              {state.outgoingApplications.length === 0 && <p className="empty-note">暂无发出的好友申请</p>}
            </div>
          ) : (
            <div className="find-user-panel">
              <div className="find-user-form">
                <label>OpenIM 用户 ID<input aria-label="查找 OpenIM 用户" value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void search(); }} placeholder="例如：imAdmin" /></label>
                <button className="primary-button compact" disabled={!query.trim() || state.searching} onClick={() => void search()}>{state.searching ? <LoaderCircle className="spin" size={16} /> : <Search size={16} />}查找</button>
              </div>
              {lookup && (
                <article className="lookup-result" data-testid={`lookup-${lookup.userID}`}>
                  <span className="avatar large">{(lookup.nickname || lookup.userID).slice(0, 1).toUpperCase()}</span>
                  <div><h3>{lookup.nickname || lookup.userID}</h3><p>{lookup.userID}</p></div>
                  {lookupIsFriend ? (
                    <button className="secondary-button" onClick={() => void openChat(lookup.userID)}><MessageCircle size={16} />发消息</button>
                  ) : lookupHasPendingOutgoing ? (
                    <span className="application-status">申请已发送</span>
                  ) : (
                    <div className="request-action">
                      <input aria-label="好友申请说明" maxLength={200} value={requestMessage} onChange={(event) => setRequestMessage(event.target.value)} />
                      <button className="primary-button" disabled={state.pendingOperations.includes(`add:${lookup.userID}`)} onClick={() => void mutate(() => controller.addFriend(lookup.userID, requestMessage))}>
                        {state.pendingOperations.includes(`add:${lookup.userID}`) ? <LoaderCircle className="spin" size={16} /> : <UserPlus size={16} />}添加好友
                      </button>
                    </div>
                  )}
                </article>
              )}
              {state.lookupAttempted && !lookup && !state.error && <p className="empty-note">未找到该用户</p>}
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
