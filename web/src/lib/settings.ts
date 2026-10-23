// SPDX-License-Identifier: AGPL-3.0-or-later
// The monitor form's fields for each type, and their conversion to and
// from the settings the API takes.

/** Shows a field only while another field has one of some values. */
export type When = { key: string; values: string[] };

type Base = { key: string; label: string; help?: string; when?: When };

export type Field = Base &
  (
    | { kind: 'text' | 'url' | 'textarea'; placeholder?: string; rows?: number }
    /** Never shown again once saved; left empty, a saved one is kept. */
    | { kind: 'secret'; placeholder?: string; rows?: number }
    | { kind: 'number'; min: number; max: number; placeholder?: string }
    | { kind: 'check' }
    | { kind: 'select'; options: string[] }
  );

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
  { key: 'max_redirects', label: 'Maximum redirects', kind: 'number', min: 0, max: 20, placeholder: '10' },
  { key: 'ignore_tls', label: 'Ignore TLS certificate errors', kind: 'check' },
  {
    key: 'notify_cert_expiry',
    label: 'Warn before the certificate expires',
    kind: 'check',
    help: 'At the days set in Settings → Notifications',
  },
];

const statusCodes: Field = {
  key: 'accepted_status_codes',
  label: 'Accepted status codes',
  kind: 'text',
  placeholder: '200-299',
  help: 'Codes or ranges, separated by commas',
};
const ignoreTLS: Field = { key: 'ignore_tls', label: 'Ignore TLS certificate errors', kind: 'check' };
const port = (placeholder: string): Field => ({ key: 'port', label: 'Port', kind: 'number', min: 1, max: 65535, placeholder });
const v3 = (f: Field): Field => ({ ...f, when: { key: 'version', values: ['3'] } });
const connection = (placeholder: string, help: string): Field => ({
  key: 'connection_string',
  label: 'Connection string',
  kind: 'secret',
  placeholder,
  help,
});
const query = (placeholder: string): Field => ({ key: 'query', label: 'Query', kind: 'textarea', placeholder, help: 'Must run without an error' });
const operators = ['==', '!=', '<', '<=', '>', '>=', 'contains'];

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
    { key: 'count', label: 'Pings per check', kind: 'number', min: 1, max: 10, placeholder: '1' },
    { key: 'packet_size', label: 'Packet size (bytes)', kind: 'number', min: 8, max: 1400, placeholder: '56' },
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
    { key: 'resolver_port', label: 'DNS server port', kind: 'number', min: 1, max: 65535, placeholder: '53' },
    { key: 'expected', label: 'A record must contain', kind: 'text', help: 'Optional' },
  ],
  'websocket-upgrade': [
    { key: 'url', label: 'URL', kind: 'url', placeholder: 'wss://example.com/socket' },
    { key: 'headers', label: 'Headers', kind: 'textarea', help: 'One per line: Name: value' },
    { key: 'subprotocol', label: 'Subprotocol', kind: 'text', help: 'Optional' },
    ignoreTLS,
    { key: 'ignore_accept', label: 'Do not check the Sec-WebSocket-Accept answer', kind: 'check' },
  ],
  smtp: [
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'mail.example.com' },
    { key: 'security', label: 'Security', kind: 'select', options: ['starttls', 'tls', 'none'], help: 'tls: TLS from the start, as on port 465' },
    port('25, or 465 with tls'),
    ignoreTLS,
  ],
  ntp: [
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'time.example.com' },
    port('123'),
    { key: 'max_offset_ms', label: 'Largest clock offset (ms)', kind: 'number', min: 0, max: 3600000, placeholder: 'any' },
  ],
  radius: [
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'radius.example.com' },
    port('1812'),
    { key: 'secret', label: 'Shared secret', kind: 'secret' },
    { key: 'username', label: 'User name', kind: 'text' },
    { key: 'password', label: 'Password', kind: 'secret' },
    { key: 'nas_identifier', label: 'NAS identifier', kind: 'text', placeholder: 'eyrie' },
    { key: 'called_station_id', label: 'Called-Station-Id', kind: 'text', help: 'Optional' },
    { key: 'calling_station_id', label: 'Calling-Station-Id', kind: 'text', help: 'Optional' },
  ],
  snmp: [
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'switch.example.com' },
    port('161'),
    { key: 'version', label: 'SNMP version', kind: 'select', options: ['2c', '1', '3'] },
    { key: 'community', label: 'Community', kind: 'secret', placeholder: 'public', when: { key: 'version', values: ['1', '2c'] } },
    { key: 'oid', label: 'OID', kind: 'text', placeholder: '1.3.6.1.2.1.1.3.0' },
    { key: 'operator', label: 'Comparison', kind: 'select', options: ['==', '!=', '<', '<=', '>', '>=', 'contains'] },
    { key: 'expected', label: 'Expected value', kind: 'text', help: 'Empty: any value' },
    v3({ key: 'username', label: 'User name', kind: 'text' }),
    v3({ key: 'auth_protocol', label: 'Authentication', kind: 'select', options: ['none', 'MD5', 'SHA', 'SHA-224', 'SHA-256', 'SHA-384', 'SHA-512'] }),
    v3({ key: 'auth_password', label: 'Authentication password', kind: 'secret' }),
    v3({ key: 'priv_protocol', label: 'Privacy', kind: 'select', options: ['none', 'DES', 'AES'] }),
    v3({ key: 'priv_password', label: 'Privacy password', kind: 'secret' }),
    v3({ key: 'context_name', label: 'Context name', kind: 'text', help: 'Optional' }),
  ],
  docker: [
    { key: 'container', label: 'Container', kind: 'text', placeholder: 'shop', help: 'Its name or ID' },
    {
      key: 'docker_host',
      label: 'Docker host',
      kind: 'text',
      placeholder: 'unix:///var/run/docker.sock',
      help: 'unix:///path, tcp://host:port or https://host:port',
    },
    ignoreTLS,
  ],
  'sip-options': [
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'pbx.example.com' },
    { key: 'transport', label: 'Transport', kind: 'select', options: ['udp', 'tcp', 'tls'] },
    port('5060, or 5061 with tls'),
    statusCodes,
    ignoreTLS,
  ],
  'tailscale-ping': [{ key: 'host', label: 'Tailscale node', kind: 'text', placeholder: 'nas', help: 'Its name or Tailscale IP address' }],
  'system-service': [{ key: 'service', label: 'Service', kind: 'text', placeholder: 'nginx', help: 'systemd on Linux, services on Windows' }],
  pm2: [{ key: 'process', label: 'PM2 process', kind: 'text', placeholder: 'api', help: 'Its name or id' }],
  globalping: [
    { key: 'kind', label: 'Measurement', kind: 'select', options: ['ping', 'http'] },
    { key: 'target', label: 'Target', kind: 'text', placeholder: 'example.com, or https://example.com/health for http' },
    { key: 'location', label: 'Probe location', kind: 'text', placeholder: 'Europe', help: 'A continent, country, city, network or ASN; empty: anywhere' },
    { key: 'packets', label: 'Packets', kind: 'number', min: 1, max: 16, placeholder: '3', when: { key: 'kind', values: ['ping'] } },
    { key: 'method', label: 'Method', kind: 'select', options: ['HEAD', 'GET', 'OPTIONS'], when: { key: 'kind', values: ['http'] } },
    { ...statusCodes, when: { key: 'kind', values: ['http'] } },
    { key: 'token', label: 'Globalping API token', kind: 'secret', help: 'Optional: for more measurements an hour' },
  ],
  sftp: [
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'files.example.com' },
    port('22'),
    { key: 'username', label: 'User name', kind: 'text' },
    { key: 'password', label: 'Password', kind: 'secret', help: 'A password, a private key or both' },
    { key: 'private_key', label: 'Private key', kind: 'secret', rows: 4, placeholder: 'An OpenSSH or PEM private key' },
    { key: 'passphrase', label: 'Private key passphrase', kind: 'secret' },
    { key: 'path', label: 'Path that must exist', kind: 'text', placeholder: '/upload', help: 'Optional' },
    { key: 'host_key', label: 'Host key fingerprint', kind: 'text', placeholder: 'SHA256:…', help: 'Empty: any key (the check shows it)' },
  ],
  steam: [{ key: 'host', label: 'Host', kind: 'text', placeholder: 'game.example.com' }, port('27015')],
  gamedig: [
    { key: 'protocol', label: 'Query protocol', kind: 'select', options: ['valve', 'minecraft', 'minecraft-bedrock', 'quake2', 'quake3'] },
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'game.example.com' },
    port("the protocol's usual port"),
  ],
  'grpc-keyword': [
    { key: 'address', label: 'Address', kind: 'text', placeholder: 'grpc.example.com:443' },
    { key: 'tls', label: 'TLS', kind: 'check' },
    ignoreTLS,
    { key: 'proto', label: 'Protobuf definition', kind: 'textarea', rows: 8, placeholder: 'syntax = "proto3"; …' },
    { key: 'service', label: 'Service', kind: 'text', placeholder: 'shop.v1.Health', help: 'With its package' },
    { key: 'method', label: 'Method', kind: 'text', placeholder: 'Check' },
    { key: 'body', label: 'Request (JSON)', kind: 'textarea', placeholder: '{}' },
    { key: 'metadata', label: 'Metadata', kind: 'textarea', help: 'One per line: name: value' },
    { key: 'keyword', label: 'Keyword', kind: 'text', help: 'In the answer, as JSON; empty: any answer' },
    { key: 'invert_keyword', label: 'Up when the keyword is missing', kind: 'check' },
  ],
  'real-browser': [
    { key: 'url', label: 'URL', kind: 'url', placeholder: 'https://example.com' },
    {
      key: 'browser_url',
      label: 'Browser address',
      kind: 'secret',
      placeholder: 'ws://chrome.example.com:3000',
      help: "Chrome's DevTools: a ws:// or wss:// address, or http://host:9222",
    },
    statusCodes,
    ignoreTLS,
  ],
  postgres: [
    connection('postgres://db.example.com:5432/shop?sslmode=require', 'A PostgreSQL URL or key=value string; user:password@ before the host'),
    query('SELECT 1'),
  ],
  mysql: [
    connection('mysql://db.example.com:3306/shop', 'A mysql:// URL with user:password@ before the host, or the driver\'s DSN'),
    query('SELECT 1'),
  ],
  sqlserver: [
    connection('sqlserver://db.example.com:1433?database=shop', 'A sqlserver:// URL with user:password@ before the host, or an ADO string'),
    query('SELECT 1'),
  ],
  oracledb: [connection('oracle://db.example.com:1521/service', 'An oracle:// URL with user:password@ before the host'), query('SELECT 1 FROM DUAL')],
  mongodb: [
    connection('mongodb://db.example.com:27017/shop', 'A mongodb:// or mongodb+srv:// URI; user:password@ before the host'),
    { key: 'command', label: 'Command (JSON)', kind: 'textarea', placeholder: '{"ping": 1}' },
    { key: 'json_path', label: 'JSON path', kind: 'text', placeholder: 'ok', help: 'Optional: a value in the answer to compare' },
    { key: 'operator', label: 'Comparison', kind: 'select', options: operators },
    { key: 'expected', label: 'Expected value', kind: 'text' },
  ],
  redis: [connection('redis://cache.example.com:6379/0', 'redis://, or rediss:// for TLS; :password@ before the host'), ignoreTLS],
  mqtt: [
    { key: 'host', label: 'Host', kind: 'text', placeholder: 'mq.example.com' },
    port('1883, or 8883 with TLS'),
    { key: 'tls', label: 'TLS', kind: 'check' },
    ignoreTLS,
    { key: 'username', label: 'User name', kind: 'text', help: 'Optional' },
    { key: 'password', label: 'Password', kind: 'secret' },
    { key: 'topic', label: 'Topic', kind: 'text', placeholder: 'shop/status', help: 'Wildcards (+, #) work' },
    { key: 'check', label: 'Check', kind: 'select', options: ['keyword', 'json-query'] },
    { key: 'keyword', label: 'Keyword', kind: 'text', help: 'Empty: any message', when: { key: 'check', values: ['keyword'] } },
    { key: 'json_path', label: 'JSON path', kind: 'text', placeholder: '$.state', when: { key: 'check', values: ['json-query'] } },
    { key: 'operator', label: 'Comparison', kind: 'select', options: operators, when: { key: 'check', values: ['json-query'] } },
    { key: 'expected', label: 'Expected value', kind: 'text', when: { key: 'check', values: ['json-query'] } },
  ],
  rabbitmq: [
    {
      key: 'nodes',
      label: 'Management API URLs',
      kind: 'text',
      placeholder: 'http://rabbit.example.com:15672',
      help: 'One or more, separated by commas; tried in order',
    },
    { key: 'username', label: 'User name', kind: 'text' },
    { key: 'password', label: 'Password', kind: 'secret' },
    ignoreTLS,
  ],
  'kafka-producer': [
    { key: 'brokers', label: 'Brokers', kind: 'text', placeholder: 'kafka.example.com:9092', help: 'host:port, separated by commas' },
    { key: 'topic', label: 'Topic', kind: 'text', placeholder: 'checks' },
    { key: 'message', label: 'Message', kind: 'text', placeholder: 'Eyrie check' },
    { key: 'tls', label: 'TLS', kind: 'check' },
    ignoreTLS,
    { key: 'sasl', label: 'SASL', kind: 'select', options: ['none', 'plain', 'scram-sha-256', 'scram-sha-512'] },
    { key: 'username', label: 'User name', kind: 'text', when: { key: 'sasl', values: ['plain', 'scram-sha-256', 'scram-sha-512'] } },
    { key: 'password', label: 'Password', kind: 'secret', when: { key: 'sasl', values: ['plain', 'scram-sha-256', 'scram-sha-512'] } },
    { key: 'allow_auto_topic_creation', label: 'Create the topic if it does not exist', kind: 'check' },
  ],
};

/** Whether a field shows, given the form's values. */
export function shown(f: Field, values: Values): boolean {
  return !f.when || f.when.values.includes(String(values[f.when.key] ?? ''));
}

/** Map fields: one "name: value" per line. */
const maps = ['headers', 'metadata'];
/** List fields: values separated by commas. */
const lists = ['accepted_status_codes', 'nodes', 'brokers'];

/** Form values: every field as text or a boolean. */
export type Values = Record<string, string | boolean>;

/** Turns saved settings into form values. */
export function toValues(type: string, settings: Record<string, unknown>): Values {
  const out: Values = {};
  for (const f of fields[type] ?? []) {
    const v = settings[f.key];
    if (f.kind === 'check') {
      out[f.key] = v === true;
    } else if (f.kind === 'secret') {
      out[f.key] = '';
    } else if (f.kind === 'select' && (v === undefined || v === null || v === '')) {
      out[f.key] = f.options[0];
    } else if (maps.includes(f.key)) {
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

/**
 * Turns form values into settings; empty fields and hidden ones are left
 * out, so the server's defaults apply and saved secrets are kept.
 */
export function toSettings(type: string, values: Values): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields[type] ?? []) {
    if (!shown(f, values)) continue;
    const v = values[f.key];
    if (f.kind === 'check') {
      out[f.key] = v === true;
      continue;
    }
    // Secrets are sent as typed: spaces can be part of a password.
    const text = f.kind === 'secret' ? String(v ?? '') : String(v ?? '').trim();
    if (text === '') continue;
    if (f.kind === 'number') {
      out[f.key] = Number(text);
    } else if (maps.includes(f.key)) {
      const headers: Record<string, string> = {};
      for (const line of text.split('\n')) {
        const i = line.indexOf(':');
        if (i > 0) headers[line.slice(0, i).trim()] = line.slice(i + 1).trim();
      }
      out[f.key] = headers;
    } else if (lists.includes(f.key)) {
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
