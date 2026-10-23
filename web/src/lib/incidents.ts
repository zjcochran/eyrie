// SPDX-License-Identifier: AGPL-3.0-or-later
// Incidents on status pages, for people.
import type { IncidentStatus } from './api.ts';

export const incidentStatuses: IncidentStatus[] = ['investigating', 'identified', 'monitoring', 'resolved'];

export const incidentLabels: Record<IncidentStatus, string> = {
  investigating: 'Investigating',
  identified: 'Identified',
  monitoring: 'Monitoring',
  resolved: 'Resolved',
};

/** The badge URLs of a monitor, for README files and dashboards. */
export function badgeURLs(origin: string, id: number): { label: string; url: string }[] {
  const base = `${origin}/api/badge/${id}`;
  return [
    { label: 'Status', url: `${base}/status` },
    { label: 'Uptime, 24 hours', url: `${base}/uptime` },
    { label: 'Uptime, 30 days', url: `${base}/uptime?period=30d` },
    { label: 'Uptime, 1 year', url: `${base}/uptime?period=1y` },
    { label: 'Response time', url: `${base}/response` },
    { label: 'Certificate expiry', url: `${base}/cert` },
  ];
}
