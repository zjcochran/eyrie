// SPDX-License-Identifier: AGPL-3.0-or-later
// The monitor form's fields for each type, and their conversion to and
// from the settings the API takes.

export type Field =
  | { key: string; label: string; kind: 'text' | 'url' | 'textarea'; placeholder?: string; help?: string }
  | { key: string; label: string; kind: 'number'; min: number; max: number; help?: string }
  | { key: string; label: string; kind: 'check'; help?: string }
  | { key: string; label: string; kind: 'select'; options: string[]; help?: string };

const http: Field[] = [
  { key: 'url', label: 'URL', kind: 'url', placeholder: 'https://example.com/health' },
  { key: 'method', label: 'Method', kind: 'select', options: ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'] },
  { key: 'headers', label: 'Headers', kind: 'textarea', placeholder: 'Authorization: Bearer …', help: 'One per line: Name: value' },
  { key: 'body', label: 'Body', kind: 'textarea' },
  {
    key: 'accepted_status_codes',
    label: 'Accepted status codes',
    kind: 'text',
    placeholder: '200-299',
    help: 'Codes or ranges, separated by commas',
  },
  { key: 'max_redirects', label: 'Maximum redirects', kind: 'number', min: 0, max: 20 },
  { key: 'ignore_tls', label: 'Ignore TLS certificate errors', kind: 'check' },
];

/** The fields of each type that has settings. */
export const fields: Record<string, Field[]> = {
  http,
  keyword: [
    ...http,
    { key: 'keyword', label: 'Keyword', kind: 'text', help: 'Case-sensitive' },
    { key: 'invert_keyword', label: 'Up when the keyword is missing', kind: 'check' },
  ],
  'json-query': [
    ...http,
    { key: 'json_path', label: 'JSON path', kind: 'text', placeholder: '$.status', help: 'Such as $.data.items[0].state' },
    { key: 'json_operator', label: 'Comparison', kind: 'select', options: ['==', '!=', '<', '<=', '>', '>=', 'contains'] },
    { key: 'expected_value', label: 'Expected value', kind: 'text' },
  ],
  port: [
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'db.example.com' },
    { key: 'port', label: 'Port', kind: 'number', min: 1, max: 65535 },
  ],
  ping: [
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'router.example.com' },
    { key: 'count', label: 'Pings per check', kind: 'number', min: 1, max: 10 },
    { key: 'packet_size', label: 'Packet size (bytes)', kind: 'number', min: 8, max: 1400 },
  ],
  dns: [
    { key: 'host', label: 'Name to look up', kind: 'text', placeholder: 'example.com' },
    {
      key: 'record_type',
      label: 'Record type',
      kind: 'select',
      options: ['A', 'AAAA', 'CAA', 'CNAME', 'MX', 'NS', 'PTR', 'SOA', 'SRV', 'TXT'],
    },
    { key: 'resolver', label: 'DNS server', kind: 'text', placeholder: '1.1.1.1' },
    { key: 'resolver_port', label: 'DNS server port', kind: 'number', min: 1, max: 65535 },
    { key: 'expected', label: 'A record must contain', kind: 'text', help: 'Optional' },
  ],
};

/** Form values: every field as text or a boolean. */
export type Values = Record<string, string | boolean>;

/** Turns saved settings into form values. */
export function toValues(type: string, settings: Record<string, unknown>): Values {
  const out: Values = {};
  for (const f of fields[type] ?? []) {
    const v = settings[f.key];
    if (f.kind === 'check') {
      out[f.key] = v === true;
    } else if (f.key === 'headers') {
      out[f.key] = Object.entries((v ?? {}) as Record<string, string>)
        .map(([k, val]) => `${k}: ${val}`)
        .join('\n');
    } else if (Array.isArray(v)) {
      out[f.key] = v.join(', ');
    } else {
      out[f.key] = v === undefined || v === null ? '' : String(v);
    }
  }
  return out;
}

/** Turns form values into settings; empty fields are left out, so the server's defaults apply. */
export function toSettings(type: string, values: Values): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields[type] ?? []) {
    const v = values[f.key];
    if (f.kind === 'check') {
      out[f.key] = v === true;
      continue;
    }
    const text = String(v ?? '').trim();
    if (text === '') continue;
    if (f.kind === 'number') {
      out[f.key] = Number(text);
    } else if (f.key === 'headers') {
      const headers: Record<string, string> = {};
      for (const line of text.split('\n')) {
        const i = line.indexOf(':');
        if (i > 0) headers[line.slice(0, i).trim()] = line.slice(i + 1).trim();
      }
      out[f.key] = headers;
    } else if (f.key === 'accepted_status_codes') {
      out[f.key] = text
        .split(',')
        .map((c) => c.trim())
        .filter(Boolean);
    } else {
      out[f.key] = text;
    }
  }
  return out;
}
