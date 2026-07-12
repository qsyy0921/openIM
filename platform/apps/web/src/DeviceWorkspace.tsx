import { AlertCircle, Check, Laptop, LogOut, RefreshCw, ShieldCheck, X } from "lucide-react";
import { useMemo, useState } from "react";

import { type DeviceController, type DeviceState, platformLabel } from "./device";

type Props = { controller: DeviceController; state: DeviceState };

function formatEnrollmentTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

export function DeviceWorkspace({ controller, state }: Props) {
  const [confirmPlatform, setConfirmPlatform] = useState<number | null>(null);
  const target = useMemo(
    () => state.devices.find((device) => device.platform_id === confirmPlatform && device.status === "active"),
    [confirmPlatform, state.devices]
  );

  const confirmLogout = async () => {
    if (!target) return;
    try {
      await controller.logout(target.platform_id);
      setConfirmPlatform(null);
    } catch {
      // DeviceState exposes the exact operation failure.
    }
  };

  return (
    <section className="device-layout" aria-labelledby="device-title">
      <header className="device-header">
        <div>
          <h1 id="device-title">设备与登录</h1>
          <p>管理当前账号已登记的终端与 OpenIM 平台会话</p>
        </div>
        <button className="subtle-icon-button" title="刷新设备状态" aria-label="刷新设备状态" disabled={state.loading} onClick={() => void controller.refresh()}>
          <RefreshCw size={17} className={state.loading ? "spin" : undefined} />
        </button>
      </header>

      {state.error && (
        <div className="device-error" role="alert">
          <AlertCircle size={17} /><span>{state.error}</span>
          <button onClick={() => controller.clearError()} aria-label="关闭错误"><X size={15} /></button>
        </div>
      )}

      <div className="device-scroll">
        <div className="device-summary">
          <ShieldCheck size={19} />
          <div><strong>平台级登录状态</strong><span>同一平台的多个设备由 OpenIM 统一管理，在线状态不代表某一台设备的最后活动。</span></div>
        </div>

        <div className="device-table" role="table" aria-label="已登记设备">
          <div className="device-table-head" role="row">
            <span>终端</span><span>登记状态</span><span>平台连接</span><span>登记更新时间</span><span>操作</span>
          </div>
          {state.loading && state.devices.length === 0 ? (
            <div className="device-empty"><RefreshCw className="spin" size={19} />正在读取设备状态</div>
          ) : state.devices.length === 0 ? (
            <div className="device-empty"><Laptop size={20} />没有设备登记</div>
          ) : state.devices.map((device) => {
            const actionable = device.status === "active" && device.online && !device.current;
            const busy = state.loggingOutPlatformID === device.platform_id;
            return (
              <div className="device-row" role="row" key={`${device.platform_id}:${device.device_id}`}>
                <div className="device-name" role="cell">
                  <span className="device-icon"><Laptop size={19} /></span>
                  <span><strong>{platformLabel(device.platform_id)}</strong><small>{device.device_id}</small></span>
                  {device.current && <b>当前</b>}
                </div>
                <span className={`device-state ${device.status}`} role="cell">{device.status === "active" ? <Check size={14} /> : <X size={14} />}{device.status === "active" ? "有效" : "已停用"}</span>
                <span className={`device-state ${device.online ? "online" : "offline"}`} role="cell"><i />{device.online ? "平台在线" : "平台离线"}</span>
                <span className="device-time" role="cell">{formatEnrollmentTime(device.updated_at)}</span>
                <span role="cell">
                  <button className="device-logout" disabled={!actionable || state.loggingOutPlatformID !== null} onClick={() => setConfirmPlatform(device.platform_id)} title={device.current ? "当前平台不能在此注销" : actionable ? "注销该平台" : "该平台当前不可注销"}>
                    {busy ? <RefreshCw className="spin" size={15} /> : <LogOut size={15} />}
                    {busy ? "注销中" : "注销"}
                  </button>
                </span>
              </div>
            );
          })}
        </div>

        {state.lastCorrelationID && <p className="device-correlation">最近操作关联 ID：<code>{state.lastCorrelationID}</code></p>}
      </div>

      {target && (
        <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && state.loggingOutPlatformID === null) setConfirmPlatform(null); }}>
          <section className="device-confirm" role="dialog" aria-modal="true" aria-labelledby="device-confirm-title">
            <header><div><h2 id="device-confirm-title">注销 {platformLabel(target.platform_id)} 平台</h2><p>该操作会使此账号在该平台上的全部 OpenIM 连接退出。</p></div><button className="subtle-icon-button" aria-label="关闭" disabled={state.loggingOutPlatformID !== null} onClick={() => setConfirmPlatform(null)}><X size={17} /></button></header>
            <dl><div><dt>平台</dt><dd>{platformLabel(target.platform_id)}</dd></div><div><dt>设备登记</dt><dd>{target.device_id}</dd></div></dl>
            <footer><button className="secondary-button" disabled={state.loggingOutPlatformID !== null} onClick={() => setConfirmPlatform(null)}>取消</button><button className="danger-button" disabled={state.loggingOutPlatformID !== null} onClick={() => void confirmLogout()}>{state.loggingOutPlatformID !== null ? <RefreshCw className="spin" size={16} /> : <LogOut size={16} />}确认注销</button></footer>
          </section>
        </div>
      )}
    </section>
  );
}
