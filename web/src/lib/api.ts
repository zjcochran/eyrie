// Calls to Eyrie's JSON API (/api/v1), documented in
// docs/technical/http-interface.md.

/** What GET /api/v1/info answers. */
export interface Info {
  name: string;
  version: string;
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

export interface Token {
  id: number;
  name: string;
  created_at: string;
  created_by: string;
  last_used: string | null;
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

  tokens = () => this.request<Token[]>('GET', '/api/v1/tokens');
  createToken = (name: string) => this.request<Token & { token: string }>('POST', '/api/v1/tokens', { name });
  deleteToken = (id: number) => this.request<void>('DELETE', `/api/v1/tokens/${id}`);
}

/** The message of an error, for people. */
export function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
