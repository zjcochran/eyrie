// SPDX-License-Identifier: AGPL-3.0-or-later
// The fields each kind of notification channel uses, for the form.

export type ChannelField = 'url' | 'token' | 'user' | 'to' | 'from';

export interface FieldInfo {
  label: string;
  placeholder?: string;
  required: boolean;
  secret: boolean;
}

const fields: Record<string, Partial<Record<ChannelField, FieldInfo>>> = {
  ntfy: {
    url: { label: 'Topic URL', placeholder: 'https://ntfy.example.com/eyrie-alerts', required: true, secret: true },
    token: { label: 'Access token (protected topics)', required: false, secret: true },
  },
  gotify: {
    url: { label: 'Server URL', placeholder: 'https://gotify.example.com', required: true, secret: true },
    token: { label: 'Application token', required: true, secret: true },
  },
  pushover: {
    token: { label: 'Application API token', required: true, secret: true },
    user: { label: 'User key', required: true, secret: true },
  },
  telegram: {
    token: { label: 'Bot token', placeholder: '123456:ABC…', required: true, secret: true },
    to: { label: 'Chat ID', placeholder: '-1001234567890', required: true, secret: false },
    url: { label: 'Bot API server (optional)', placeholder: 'https://api.telegram.org', required: false, secret: true },
  },
  discord: {
    url: { label: 'Webhook URL', placeholder: 'https://discord.com/api/webhooks/…', required: true, secret: true },
  },
  email: {
    url: { label: 'SMTP server', placeholder: 'smtp://mail.example.com:587', required: true, secret: true },
    user: { label: 'Username (optional)', required: false, secret: true },
    token: { label: 'Password', required: false, secret: true },
    from: { label: 'From', placeholder: 'Eyrie <eyrie@example.com>', required: true, secret: false },
    to: { label: 'To', placeholder: 'alice@example.com, bob@example.com', required: true, secret: false },
  },
  webhook: {
    url: { label: 'URL', placeholder: 'https://hooks.example.com/eyrie', required: true, secret: true },
    token: { label: 'Signing secret (optional; X-Hub-Signature-256)', required: false, secret: true },
  },
};

const order: ChannelField[] = ['url', 'token', 'user', 'to', 'from'];

/** The fields a kind uses, in form order. */
export function fieldsFor(kind: string): [ChannelField, FieldInfo][] {
  const f = fields[kind] ?? {};
  return order.filter((k) => f[k]).map((k) => [k, f[k] as FieldInfo]);
}
