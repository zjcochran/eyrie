// Calls to Eyrie's JSON API (/api/v1), documented in
// docs/technical/http-interface.md.

/** What GET /api/v1/info answers. */
export interface Info {
  name: string;
  version: string;
}

/** Fetches the server's name and version. */
export async function getInfo(fetchFn: typeof fetch = fetch.bind(globalThis)): Promise<Info> {
  const resp = await fetchFn('/api/v1/info', { headers: { Accept: 'application/json' } });
  if (!resp.ok) {
    throw new Error(`GET /api/v1/info answered ${resp.status}`);
  }
  return (await resp.json()) as Info;
}
