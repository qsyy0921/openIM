import { Check, LoaderCircle, Search, UserPlus, X } from "lucide-react";
import { useMemo, useState } from "react";

import type { ContactController, ContactState } from "./contact";

export type MemberCandidate = {
  userID: string;
  nickname: string;
  faceURL: string;
  source: "friend" | "lookup";
};

export function memberCandidates(state: ContactState, selfUserID: string, excludedUserIDs: string[] = []): MemberCandidate[] {
  const values = new Map<string, MemberCandidate>();
  const excluded = new Set([selfUserID, ...excludedUserIDs]);
  for (const friend of state.friends) {
    if (excluded.has(friend.userID)) continue;
    values.set(friend.userID, {
      userID: friend.userID,
      nickname: friend.remark || friend.nickname || friend.userID,
      faceURL: friend.faceURL,
      source: "friend"
    });
  }
  for (const lookup of state.lookedUpUsers) {
    if (excluded.has(lookup.userID) || values.has(lookup.userID)) continue;
    values.set(lookup.userID, {
      userID: lookup.userID,
      nickname: lookup.nickname || lookup.userID,
      faceURL: lookup.faceURL,
      source: "lookup"
    });
  }
  return [...values.values()].sort((left, right) => left.nickname.localeCompare(right.nickname));
}

export function toggleMemberSelection(selected: string[], userID: string): string[] {
  const values = new Set(selected.filter(Boolean));
  if (values.has(userID)) values.delete(userID);
  else values.add(userID);
  return [...values];
}

type MemberPickerProps = {
  controller: ContactController;
  state: ContactState;
  selfUserID: string;
  selectedUserIDs: string[];
  excludedUserIDs?: string[];
  onChange: (selectedUserIDs: string[]) => void;
  disabled?: boolean;
};

export function MemberPicker({ controller, state, selfUserID, selectedUserIDs, excludedUserIDs = [], onChange, disabled = false }: MemberPickerProps) {
  const [query, setQuery] = useState("");
  const excludedKey = excludedUserIDs.join("\u0000");
  const candidates = useMemo(() => memberCandidates(state, selfUserID, excludedUserIDs), [state, selfUserID, excludedKey]);
  const selected = new Set(selectedUserIDs);
  const selectedCandidates = selectedUserIDs.map((userID) => candidates.find((candidate) => candidate.userID === userID)).filter((candidate): candidate is MemberCandidate => Boolean(candidate));

  const search = async () => {
    try {
      await controller.lookup(query);
    } catch {
      // ContactState exposes the exact lookup failure.
    }
  };

  return (
    <section className="member-picker" aria-label="选择群成员">
      <div className="member-search">
        <input
          aria-label="查找群成员"
          placeholder="输入准确的 OpenIM 用户 ID"
          value={query}
          disabled={disabled || state.searching}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); void search(); } }}
        />
        <button className="icon-button" type="button" aria-label="查找用户" title="查找用户" disabled={disabled || state.searching || !query.trim()} onClick={() => void search()}>
          {state.searching ? <LoaderCircle className="spin" size={17} /> : <Search size={17} />}
        </button>
      </div>

      {state.error && <div className="inline-error" role="alert"><span>{state.error}</span><button type="button" onClick={() => controller.clearError()}>关闭</button></div>}

      {selectedCandidates.length > 0 && (
        <div className="selected-members" aria-label="已选群成员">
          {selectedCandidates.map((candidate) => (
            <button key={candidate.userID} type="button" disabled={disabled} onClick={() => onChange(toggleMemberSelection(selectedUserIDs, candidate.userID))}>
              <span>{candidate.nickname}</span><X size={13} />
            </button>
          ))}
        </div>
      )}

      <div className="member-candidate-list">
        {candidates.map((candidate) => {
          const checked = selected.has(candidate.userID);
          return (
            <label className="member-candidate" key={candidate.userID}>
              <input
                type="checkbox"
                checked={checked}
                disabled={disabled}
                onChange={() => onChange(toggleMemberSelection(selectedUserIDs, candidate.userID))}
              />
              <span className="avatar">{candidate.nickname.slice(0, 1).toUpperCase()}</span>
              <span className="candidate-copy"><strong>{candidate.nickname}</strong><small>{candidate.userID}</small></span>
              <span className={`candidate-state ${checked ? "selected" : ""}`}>{checked ? <Check size={14} /> : candidate.source === "friend" ? "好友" : <UserPlus size={14} />}</span>
            </label>
          );
        })}
        {!state.loading && candidates.length === 0 && <p className="empty-note">暂无好友，可按准确用户 ID 查找</p>}
      </div>
    </section>
  );
}
