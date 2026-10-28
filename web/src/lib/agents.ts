// SPDX-License-Identifier: AGPL-3.0-or-later
// How to run an agent on each platform, with its enrollment token.

export const platforms = [
  { id: 'docker', label: 'Docker' },
  { id: 'swarm', label: 'Docker Swarm' },
  { id: 'kubernetes', label: 'Kubernetes' },
  { id: 'proxmox', label: 'Proxmox VE' },
] as const;

export type Platform = (typeof platforms)[number]['id'];

const image = 'ghcr.io/zjcochran/eyrie:latest';

/** The files or commands that run an agent on a platform, reporting to url. */
export function agentSetup(platform: Platform, url: string, token: string): string {
  const env = `      EYRIE_AGENT_URL: ${url}\n      EYRIE_AGENT_TOKEN: ${token}`;
  switch (platform) {
    case 'docker':
      return `# compose.yaml on the Docker host
services:
  eyrie-agent:
    image: ${image}
    command: agent
    restart: unless-stopped
    environment:
${env}
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - eyrie-agent:/data

volumes:
  eyrie-agent:
    name: eyrie-agent`;
    case 'swarm':
      return `# eyrie-agent.yaml, then: docker stack deploy -c eyrie-agent.yaml eyrie
services:
  agent:
    image: ${image}
    command: agent
    environment:
      EYRIE_AGENT_PLATFORM: swarm
${env}
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - agent:/data
    deploy:
      replicas: 1
      placement:
        constraints: [node.role == manager]

volumes:
  agent:
    name: eyrie-agent`;
    case 'kubernetes':
      return `# deploy/kubernetes/eyrie-agent.yaml is in Eyrie's source code.
kubectl create namespace eyrie
kubectl -n eyrie create secret generic eyrie-agent \\
  --from-literal=EYRIE_AGENT_URL=${url} \\
  --from-literal=EYRIE_AGENT_TOKEN=${token}
kubectl apply -f eyrie-agent.yaml`;
    case 'proxmox':
      return `# compose.yaml on any host that reaches Proxmox's API
services:
  eyrie-agent:
    image: ${image}
    command: agent
    restart: unless-stopped
    environment:
      EYRIE_AGENT_PLATFORM: proxmox
${env}
      PROXMOX_URL: https://pve.example.com:8006
      PROXMOX_TOKEN_ID: eyrie@pve!agent
      PROXMOX_TOKEN_SECRET: the token's secret
      PROXMOX_FINGERPRINT: the certificate's SHA-256 fingerprint
    volumes:
      - eyrie-agent:/data

volumes:
  eyrie-agent:
    name: eyrie-agent`;
  }
}
