// SPDX-License-Identifier: AGPL-3.0-or-later
// What a monitor's page shows for each type: the figures at the top, what
// its last check found, its chart and the words for its status
// (docs/user/monitors.md, "A monitor's page").
import type { Detail, Status } from './api.ts';

/** A chart of the number a type's checks measure. */
export interface ValueChart {
  title: string;
  /** Shown after each value, such as 'ms'; '' for none. */
  unit: string;
}

export interface Page {
  /** The response time means something: its figure, chart and source. */
  latency: boolean;
  /** Always has a certificate card; others show one once a check saw one. */
  cert: boolean;
  /** The chart of the measured value; null for none. */
  value: ValueChart | null;
  /** Details shown as figures at the top, by name, when the check found them. */
  figures: string[];
  /** The title of the card with everything the last check found. */
  found: string;
  /** Words for up and down, and for the uptime figure. */
  up: string;
  down: string;
  uptime: string;
  /** Words for a time spent down, in the list of outages. */
  outages: string;
  outage: string;
}

const base: Page = {
  latency: true,
  cert: false,
  value: null,
  figures: [],
  found: 'Last check',
  up: 'Up',
  down: 'Down',
  uptime: 'Uptime',
  outages: 'Outages',
  outage: 'Down',
};

const web = { ...base, cert: true, found: 'Response', figures: ['Status', 'Protocol'] };
const sql = { ...base, found: 'Query', value: { title: 'First value', unit: '' }, figures: ['First value', 'Rows'] };
const game = { ...base, found: 'Server', value: { title: 'Players', unit: '' }, figures: ['Players', 'Map'] };

/** Every type's page, by type name. */
export const pages: Record<string, Page> = {
  http: web,
  keyword: { ...web, figures: ['Status', 'Keyword'] },
  'json-query': { ...web, value: { title: 'Value', unit: '' }, figures: ['Value', 'Expected'] },
  'real-browser': { ...web, found: 'Page', figures: ['Status', 'Other requests', 'Failed requests'] },
  'websocket-upgrade': { ...base, found: 'Connection', figures: ['Status', 'TLS'] },
  'grpc-keyword': { ...base, found: 'Call', figures: ['gRPC status'] },
  globalping: { ...base, found: 'Probe', figures: ['Probe', 'Network'] },
  port: { ...base, found: 'Connection', figures: ['Connected to'] },
  ping: { ...base, found: 'Pings', figures: ['Replies', 'Lost'] },
  dns: { ...base, found: 'Answer', figures: ['Server'] },
  smtp: { ...base, found: 'Mail server', figures: ['Security', 'TLS'] },
  ntp: { ...base, latency: false, found: 'Clock', value: { title: 'Clock offset', unit: 'ms' }, figures: ['Offset', 'Stratum', 'Reference'] },
  radius: { ...base, found: 'Sign-in', figures: ['Answer', 'User'] },
  snmp: { ...base, latency: false, found: 'Value', value: { title: 'Value', unit: '' }, figures: ['Value', 'Expected'] },
  'snmp-storage': { ...base, latency: false, found: 'NAS' },
  docker: { ...base, latency: false, found: 'Container', figures: ['State', 'Health', 'Restarts'], up: 'Running', down: 'Not running', outages: 'Times not running', outage: 'Not running' },
  'docker-swarm': { ...base, latency: false, found: 'Service', value: { title: 'Tasks running', unit: '' }, figures: ['State', 'Stack'] },
  kubernetes: { ...base, latency: false, found: 'Workload', value: { title: 'Ready replicas', unit: '' }, figures: ['State', 'Namespace'] },
  proxmox: { ...base, latency: false, found: 'Guest', figures: ['State', 'CPU', 'Memory'], up: 'Running', down: 'Not running', outages: 'Times not running', outage: 'Not running' },
  'sip-options': { ...base, found: 'Answer', figures: ['Status', 'Transport'] },
  'tailscale-ping': { ...base, found: 'Node', figures: ['Path', 'Address'] },
  'system-service': { ...base, latency: false, found: 'Service', figures: ['State', 'Running since', 'Memory'], up: 'Running', down: 'Not running', outages: 'Times not running', outage: 'Not running' },
  'system-resources': { ...base, latency: false, found: 'Host', figures: ['Load, 1 minute'] },
  pm2: { ...base, latency: false, found: 'Process', value: { title: 'Memory', unit: 'MiB' }, figures: ['Status', 'CPU', 'Restarts'], up: 'Online', down: 'Not online' },
  drawbridge: {
    ...base,
    latency: false,
    found: 'Internet connection',
    figures: ['ISP in use', 'Since', 'Public IP'],
    up: 'On primary',
    down: 'Failed over',
    uptime: 'On primary',
    outages: 'Failovers',
    outage: 'On a backup ISP',
  },
  sftp: { ...base, found: 'Server', figures: ['Signed in as', 'Host key checked'] },
  steam: game,
  gamedig: game,
  postgres: sql,
  mysql: sql,
  sqlserver: sql,
  oracledb: sql,
  mongodb: { ...base, found: 'Command', value: { title: 'Value', unit: '' }, figures: ['Value', 'Expected'] },
  redis: { ...base, found: 'Server', figures: ['Reply', 'Database'] },
  mqtt: { ...base, latency: false, found: 'Message', value: { title: 'Value', unit: '' }, figures: ['Value', 'Topic', 'Waited'] },
  rabbitmq: { ...base, found: 'Node', figures: ['Alarms', 'Node'] },
  'kafka-producer': { ...base, found: 'Message sent', figures: ['Partition', 'Offset'] },
  // A push's response time is only what its job reports in ping=.
  push: { ...base, found: 'Last push' },
  manual: { ...base, latency: false, found: 'Status' },
};

/** A type's page; an unknown type gets the plain one. */
export const pageOf = (type: string): Page => pages[type] ?? base;

/** The word for a status on a type's page. */
export function statusWord(p: Page, status: Status | 'paused'): string {
  if (status === 'up') return p.up;
  if (status === 'down') return p.down;
  return status.charAt(0).toUpperCase() + status.slice(1);
}

const isTime = /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/;

/** A detail's value as people read it: times in the reader's own zone. */
export function detailText(value: string): string {
  return isTime.test(value) ? new Date(value).toLocaleString() : value;
}

/** The figures a type's page shows at the top, from what its last check found. */
export function figures(p: Page, details: Detail[]): Detail[] {
  return p.figures.flatMap((name) => details.filter((d) => d.name === name).slice(0, 1));
}

/** A time spent down: from a change to down until the next change, or now. */
export interface Outage {
  from: string;
  to: string | null;
  message: string;
}

/**
 * The outages in a monitor's status changes (newest first), newest first.
 * Pending and maintenance neither start nor end one.
 */
export function outages(changes: { at: string; status: Status; message: string }[]): Outage[] {
  const out: Outage[] = [];
  let end: string | null = null;
  for (const c of changes) {
    if (c.status === 'up') end = c.at;
    else if (c.status === 'down') {
      out.push({ from: c.at, to: end, message: c.message });
      end = c.at;
    }
  }
  return out;
}

/** How long an outage lasted, in milliseconds, until now when it goes on. */
export const outageLength = (o: Outage, now: number) => (o.to ? Date.parse(o.to) : now) - Date.parse(o.from);

/** A length of time, in its two largest units: "2 h 15 min", "45 s". */
export function lasted(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} s`;
  const d = Math.floor(s / 86400);
  const h = Math.floor(s / 3600) % 24;
  const m = Math.floor(s / 60) % 60;
  if (d) return h ? `${d} d ${h} h` : `${d} d`;
  if (h) return m ? `${h} h ${m} min` : `${h} h`;
  return `${m} min`;
}

/** Whether a detail is a time, shown as how long ago. */
export const isTimeValue = (value: string) => isTime.test(value);
