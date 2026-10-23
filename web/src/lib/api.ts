// SPDX-License-Identifier: AGPL-3.0-or-later
// Calls to Eyrie's JSON API (/api/v1), documented in
// docs/technical/http-interface.md.

/** What GET /api/v1/info answers. */
export interface Info {
  name: string;
  version: string;
  license: string;
  source: string;
}

export interface User {
  id: number;
  username: string;
  role: 'admin' | 'viewer';
  disabled: boolean;
  password: boolean;
  sso: boolean;
  two_factor: boolean;
  picture: string;
  sso_picture: boolean;
  created_at: string;
  last_login: string | null;
  role_from_group: boolean;
}

export interface SSOButton {
  enabled: boolean;
  name: string;
}

/** What GET /api/v1/session answers, and a finished sign-in. */
export interface Session {
  setup: boolean;
  user: User | null;
  csrf?: string;
  sso: SSOButton;
  password_sign_in: boolean;
  version: string;
  /** Where this version's source code is (AGPL section 13); "" when unknown. */
  source: string;
}

export interface Account {
  user: User;
  identities: { issuer: string; email: string; linked_at: string }[];
  two_factor: { available: boolean; on: boolean; codes_left: number };
  sso: SSOButton;
}

export interface TwoFactorSetup {
  secret: string;
  uri: string;
  qr: string;
  sealed: string;
}

export interface SSOSettings {
  enabled: boolean;
  issuer: string;
  client_id: string;
  has_secret: boolean;
  display_name: string;
  auto_provision: boolean;
  allowed_group: string;
  admin_group: string;
  sso_only: boolean;
  callback_url: string;
}

export interface SSOSettingsChange extends Omit<SSOSettings, 'has_secret' | 'callback_url'> {
  client_secret: string;
  clear_secret: boolean;
}

export interface AnalysisSettings {
  enabled: boolean;
  /** 'api': Claude with an Anthropic API key; 'claude-code': Claude with a
   * subscription, through Claude Code; 'local': an OpenAI-compatible server. */
  provider: 'api' | 'claude-code' | 'local';
  model: string;
  has_api_key: boolean;
  base_url: string;
  has_local_api_key: boolean;
  timeout_s: number;
  default_model: string;
  /** The Claude Code download; null unless EYRIE_CLAUDE_CODE=true. */
  claude_code: { state: 'installing' | 'ready' | 'failed'; version?: string; error?: string } | null;
}

export interface AnalysisSettingsChange {
  enabled: boolean;
  provider: 'api' | 'claude-code' | 'local';
  model: string;
  base_url: string;
  timeout_s: number;
  /** The chosen provider's key: '' keeps the saved one. */
  api_key: string;
  clear_api_key: boolean;
}

export interface Token {
  id: number;
  name: string;
  created_at: string;
  created_by: string;
  last_used: string | null;
}

export type Status = 'unknown' | 'up' | 'pending' | 'down' | 'maintenance';

export interface Beat {
  monitor_id: number;
  at: string;
  status: Status;
  latency_ms: number | null;
  message: string;
  important: boolean;
}

/** A monitor's settings, as created or changed. */
export interface MonitorChange {
  name: string;
  type?: string;
  parent_id: number | null;
  active: boolean;
  interval: number;
  retry_interval: number;
  retries: number;
  resend_every: number;
  timeout: number;
  upside_down: boolean;
  description: string;
  settings: Record<string, unknown>;
  reset_push_token?: boolean;
  /** Notification channels; left out, a new monitor gets the default ones. */
  channels?: number[];
  /** Tags, each with a value ('' for none); left out, a changed monitor keeps its own. */
  tags?: { id: number; value: string }[];
}

export interface Tag {
  id: number;
  name: string;
  /** Such as #1f5fbf. */
  color: string;
  monitors: number;
  created_at: string;
}

/** A tag on a monitor, with the monitor's value for it. */
export interface MonitorTag {
  id: number;
  name: string;
  color: string;
  value: string;
}

export interface Monitor extends Omit<MonitorChange, 'type' | 'channels' | 'tags'> {
  id: number;
  channels: number[];
  tags: MonitorTag[];
  type: string;
  push_url?: string;
  created_at: string;
  updated_at: string;
  status: {
    status: Status;
    since: string | null;
    message: string;
    last_check: string | null;
    latency_ms: number | null;
    cert_expires: string | null;
    checking: boolean;
    /** The maintenance window it is in; '' for none. */
    maintenance: string;
  };
  uptime: { '24h': number | null; '30d': number | null; '1y': number | null; avg_latency_24h_ms: number | null };
  beats?: Beat[];
}

/** When a maintenance window is on; docs/user/maintenance.md. */
export interface Schedule {
  strategy: 'manual' | 'once' | 'interval' | 'weekdays' | 'monthdays' | 'cron';
  time_zone: string;
  start?: string;
  end?: string;
  from?: string;
  to?: string;
  every_days?: number;
  weekdays?: number[];
  month_days?: number[];
  cron?: string;
  duration_minutes?: number;
  first_day?: string;
  last_day?: string;
}

export interface WindowChange {
  title: string;
  description: string;
  enabled: boolean;
  schedule: Schedule;
  all_monitors: boolean;
  monitors: number[];
  tags: number[];
}

export interface Window extends WindowChange {
  id: number;
  /** on; scheduled (change: when it starts); ended; paused (turned off). */
  state: 'on' | 'scheduled' | 'ended' | 'paused';
  /** When it starts (scheduled) or ends (on); null for never. */
  change: string | null;
  created_at: string;
  updated_at: string;
}

export interface HourStats {
  start: string;
  up: number;
  down: number;
  avg_latency_ms: number | null;
}

export interface Channel {
  id: number;
  name: string;
  kind: string;
  url_host: string;
  has_url: boolean;
  has_token: boolean;
  has_user: boolean;
  to: string;
  from: string;
  enabled: boolean;
  default: boolean;
  monitors: number;
  last: { at: string; error: string } | null;
}

/** A channel as saved or tested. Empty secrets keep the saved ones. */
export interface ChannelChange {
  id?: number;
  name: string;
  kind: string;
  url: string;
  token: string;
  user: string;
  to: string;
  from: string;
  enabled: boolean;
  default: boolean;
  apply_to_all?: boolean;
  clear_url?: boolean;
  clear_token?: boolean;
  clear_user?: boolean;
}

/** An error the server answered, with its message for people. */
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

type Fetch = typeof fetch;

/** Talks to the API, sending the session's CSRF token with every change. */
export class Client {
  csrf = '';
  private readonly fetchFn: Fetch;

  constructor(fetchFn: Fetch = fetch.bind(globalThis)) {
    this.fetchFn = fetchFn;
  }

  /** Sends a request; a Blob body goes as is, anything else as JSON. */
  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { Accept: 'application/json' };
    let payload: BodyInit | undefined;
    if (body instanceof Blob) {
      payload = body;
    } else if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
      payload = JSON.stringify(body);
    }
    if (method !== 'GET' && this.csrf) {
      headers['X-CSRF-Token'] = this.csrf;
    }
    const resp = await this.fetchFn(path, { method, headers, body: payload, credentials: 'same-origin' });
    if (resp.status === 204) {
      return undefined as T;
    }
    const text = await resp.text();
    let data: unknown = undefined;
    try {
      data = text ? JSON.parse(text) : undefined;
    } catch {
      // not JSON: reported below by status
    }
    if (!resp.ok) {
      const msg = (data as { error?: string } | undefined)?.error;
      throw new ApiError(resp.status, msg || `${method} ${path} answered ${resp.status}`);
    }
    return data as T;
  }

  /** Keeps the CSRF token of a session answer. */
  private keep(s: Session): Session {
    this.csrf = s.csrf ?? '';
    return s;
  }

  info = () => this.request<Info>('GET', '/api/v1/info');
  session = async () => this.keep(await this.request<Session>('GET', '/api/v1/session'));
  setup = async (username: string, password: string) =>
    this.keep(await this.request<Session>('POST', '/api/v1/setup', { username, password }));

  /** Signs in; null means a two-factor code must follow (loginCode). */
  async login(username: string, password: string): Promise<Session | null> {
    const r = await this.request<Session | { two_factor: true }>('POST', '/api/v1/login', { username, password });
    return 'two_factor' in r ? null : this.keep(r);
  }

  loginCode = async (code: string) => this.keep(await this.request<Session>('POST', '/api/v1/login/code', { code }));

  async logout(): Promise<void> {
    await this.request<void>('POST', '/api/v1/logout');
    this.csrf = '';
  }

  account = () => this.request<Account>('GET', '/api/v1/account');
  changePassword = async (current: string, next: string) =>
    this.keep(await this.request<Session>('PUT', '/api/v1/account/password', { current, new: next }));
  twoFactorSetup = () => this.request<TwoFactorSetup>('POST', '/api/v1/account/two-factor/setup');
  twoFactorEnable = (sealed: string, code: string, password: string) =>
    this.request<{ codes: string[] }>('POST', '/api/v1/account/two-factor/enable', { sealed, code, password });
  newRecoveryCodes = (password: string) =>
    this.request<{ codes: string[] }>('POST', '/api/v1/account/two-factor/recovery-codes', { password });
  twoFactorDisable = (password: string) =>
    this.request<void>('POST', '/api/v1/account/two-factor/disable', { password });
  uploadPicture = (file: Blob) => this.request<User>('PUT', '/api/v1/account/picture', file);
  removePicture = () => this.request<User>('DELETE', '/api/v1/account/picture');
  useSSOPicture = () => this.request<User>('POST', '/api/v1/account/picture/sso');
  unlinkSSO = () => this.request<void>('DELETE', '/api/v1/account/sso');

  users = () => this.request<User[]>('GET', '/api/v1/users');
  createUser = (username: string, password: string, role: string) =>
    this.request<User>('POST', '/api/v1/users', { username, password, role });
  updateUser = (id: number, change: { role?: string; disabled?: boolean }) =>
    this.request<User>('PATCH', `/api/v1/users/${id}`, change);
  resetPassword = (id: number, password: string) =>
    this.request<void>('PUT', `/api/v1/users/${id}/password`, { password });
  resetTwoFactor = (id: number) => this.request<void>('DELETE', `/api/v1/users/${id}/two-factor`);
  deleteUser = (id: number) => this.request<void>('DELETE', `/api/v1/users/${id}`);

  ssoSettings = () => this.request<SSOSettings>('GET', '/api/v1/settings/sso');
  saveSSOSettings = (s: SSOSettingsChange) => this.request<SSOSettings>('PUT', '/api/v1/settings/sso', s);
  testSSO = (issuer: string) =>
    this.request<{ ok: boolean; message: string }>('POST', '/api/v1/settings/sso/test', { issuer });

  monitorTypes = () => this.request<{ name: string; label: string }[]>('GET', '/api/v1/monitor-types');
  monitors = (beats = 0) => this.request<Monitor[]>('GET', `/api/v1/monitors${beats ? `?beats=${beats}` : ''}`);
  monitor = (id: number) => this.request<Monitor>('GET', `/api/v1/monitors/${id}`);
  heartbeats = (id: number, since: Date) =>
    this.request<Beat[]>('GET', `/api/v1/monitors/${id}/heartbeats?since=${encodeURIComponent(since.toISOString())}`);
  hours = (id: number, days: number) => this.request<HourStats[]>('GET', `/api/v1/monitors/${id}/hours?days=${days}`);
  createMonitor = (m: MonitorChange) => this.request<Monitor>('POST', '/api/v1/monitors', m);
  updateMonitor = (id: number, m: MonitorChange) => this.request<Monitor>('PUT', `/api/v1/monitors/${id}`, m);
  deleteMonitor = (id: number) => this.request<void>('DELETE', `/api/v1/monitors/${id}`);
  pauseMonitor = (id: number) => this.request<Monitor>('POST', `/api/v1/monitors/${id}/pause`);
  resumeMonitor = (id: number) => this.request<Monitor>('POST', `/api/v1/monitors/${id}/resume`);
  checkMonitor = (id: number) => this.request<void>('POST', `/api/v1/monitors/${id}/check`);
  setManualStatus = (id: number, status: 'up' | 'down', message: string) =>
    this.request<Monitor>('POST', `/api/v1/monitors/${id}/status`, { status, message });
  tags = () => this.request<Tag[]>('GET', '/api/v1/tags');
  createTag = (name: string, color: string) => this.request<Tag>('POST', '/api/v1/tags', { name, color });
  updateTag = (id: number, name: string, color: string) =>
    this.request<Tag>('PUT', `/api/v1/tags/${id}`, { name, color });
  deleteTag = (id: number) => this.request<void>('DELETE', `/api/v1/tags/${id}`);
  windows = () => this.request<Window[]>('GET', '/api/v1/maintenance');
  createWindow = (w: WindowChange) => this.request<Window>('POST', '/api/v1/maintenance', w);
  updateWindow = (id: number, w: WindowChange) => this.request<Window>('PUT', `/api/v1/maintenance/${id}`, w);
  deleteWindow = (id: number) => this.request<void>('DELETE', `/api/v1/maintenance/${id}`);
  channelKinds = () => this.request<{ id: string; name: string }[]>('GET', '/api/v1/channel-kinds');
  channels = () => this.request<Channel[]>('GET', '/api/v1/channels');
  createChannel = (c: ChannelChange) => this.request<Channel>('POST', '/api/v1/channels', c);
  updateChannel = (id: number, c: ChannelChange) => this.request<Channel>('PUT', `/api/v1/channels/${id}`, c);
  deleteChannel = (id: number) => this.request<void>('DELETE', `/api/v1/channels/${id}`);
  testChannel = (c: ChannelChange) =>
    this.request<{ ok: boolean; message: string }>('POST', '/api/v1/channels/test', c);
  notifySettings = () => this.request<{ cert_expiry_days: number[] }>('GET', '/api/v1/settings/notifications');
  saveNotifySettings = (days: number[]) =>
    this.request<{ cert_expiry_days: number[] }>('PUT', '/api/v1/settings/notifications', { cert_expiry_days: days });
  analysisSettings = () => this.request<AnalysisSettings>('GET', '/api/v1/settings/analysis');
  saveAnalysisSettings = (s: AnalysisSettingsChange) =>
    this.request<AnalysisSettings>('PUT', '/api/v1/settings/analysis', s);
  testAnalysis = (s: AnalysisSettingsChange) =>
    this.request<{ ok: boolean; message: string }>('POST', '/api/v1/settings/analysis/test', s);
  /** The models the form's provider offers, or why they could not be listed. */
  analysisModels = (s: AnalysisSettingsChange) =>
    this.request<{ models: string[]; error?: string }>('POST', '/api/v1/settings/analysis/models', s);
  retention = () => this.request<{ heartbeat_days: number }>('GET', '/api/v1/settings/retention');
  saveRetention = (days: number) =>
    this.request<{ heartbeat_days: number }>('PUT', '/api/v1/settings/retention', { heartbeat_days: days });

  tokens = () => this.request<Token[]>('GET', '/api/v1/tokens');
  createToken = (name: string) => this.request<Token & { token: string }>('POST', '/api/v1/tokens', { name });
  deleteToken = (id: number) => this.request<void>('DELETE', `/api/v1/tokens/${id}`);
}

/** The message of an error, for people. */
export function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
