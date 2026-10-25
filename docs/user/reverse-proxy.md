# Reverse proxy examples

Configurations for the common reverse proxies, each serving Eyrie over
HTTPS at `https://eyrie.example.com`. Whichever you use:

- Give Eyrie its own host name. It is served from the root (`/`) and
  does not work under a path such as `https://example.com/eyrie/`.
- Tell Eyrie its public address and the proxy's address
  ([Behind a reverse proxy](installation.md#behind-a-reverse-proxy)):

  ```yaml
      environment:
        EYRIE_BASE_URL: https://eyrie.example.com
        EYRIE_TRUSTED_PROXIES: 192.0.2.10
  ```

- When the proxy runs on the same host, publish Eyrie's port on the
  loopback address only, so the proxy is the only way in:

  ```yaml
      ports:
        - "127.0.0.1:8080:8080"
  ```

What the proxy has to get right:

| What | Why |
| --- | --- |
| Pass `Host`, and set `X-Forwarded-For` and `X-Forwarded-Proto` | The visitor's address for the log, the audit log and the sign-in rate limit; HTTPS for the `Secure` session cookie |
| Allow request bodies of 129 MiB | Restoring a [backup file](backup-and-restore.md) uploads it (up to 128 MiB) |
| Don't buffer responses, and keep idle connections open for more than 25 s | The dashboard's live updates are a long `text/event-stream` response (`/api/v1/events`) with a keepalive every 25 seconds. Eyrie sends `X-Accel-Buffering: no`, which nginx obeys |

No WebSocket setup is needed.

## Finding the proxy's address

`EYRIE_TRUSTED_PROXIES` is the proxy's address as Eyrie sees it, which in
Docker is often not the address you expect. To find it, start Eyrie
without `EYRIE_TRUSTED_PROXIES`, sign in through the proxy and open
**Settings → Audit log**: the address on your sign-in is the proxy's. Set
it (or its network's range, shown by `docker network inspect`, if the
proxy's address can change), recreate the container and sign in again. The
audit log should now show your own address.

## nginx

```nginx
server {
    listen 80;
    server_name eyrie.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    http2 on;
    server_name eyrie.example.com;

    ssl_certificate     /etc/nginx/certs/eyrie.example.com.crt;
    ssl_certificate_key /etc/nginx/certs/eyrie.example.com.key;

    # Backup restores upload up to 128 MiB (the default is 1 MiB).
    client_max_body_size 129m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
    }
}
```

nginx's default `proxy_read_timeout` (60 s) outlasts the event stream's
keepalive, and Eyrie's `X-Accel-Buffering: no` turns buffering off for the
stream, so neither needs changing.

## Caddy

```caddy
eyrie.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy gets the certificate, redirects HTTP to HTTPS, sets
`X-Forwarded-For`, `X-Forwarded-Proto` and `X-Forwarded-Host`, has no
request body limit, and sends `text/event-stream` responses on as they
arrive. Nothing else is needed.

## Traefik

With Traefik in Docker, reading labels from the Docker provider, on a
network it shares with Eyrie:

```yaml
services:
  eyrie:
    image: ghcr.io/zjcochran/eyrie:latest
    restart: unless-stopped
    environment:
      TZ: Etc/UTC
      EYRIE_BASE_URL: https://eyrie.example.com
      EYRIE_TRUSTED_PROXIES: 198.51.100.0/24 # the proxy network's range
    volumes:
      - eyrie-data:/data
    networks:
      - proxy
    labels:
      - traefik.enable=true
      - traefik.docker.network=proxy
      - traefik.http.routers.eyrie.rule=Host(`eyrie.example.com`)
      - traefik.http.routers.eyrie.entrypoints=websecure
      - traefik.http.routers.eyrie.tls.certresolver=letsencrypt
      - traefik.http.services.eyrie.loadbalancer.server.port=8080

volumes:
  eyrie-data:

networks:
  proxy:
    external: true
```

No `ports:`: Traefik reaches Eyrie over the `proxy` network. The entry
point (`websecure`) and the certificate resolver (`letsencrypt`) are the
names from your Traefik configuration. Traefik sets the `X-Forwarded-*`
headers, has no body limit unless you add one, and sends
`text/event-stream` responses on as they arrive.

## Apache httpd

With `mod_ssl`, `mod_proxy`, `mod_proxy_http` and `mod_headers` on:

```apache
<VirtualHost *:80>
    ServerName eyrie.example.com
    Redirect permanent / https://eyrie.example.com/
</VirtualHost>

<VirtualHost *:443>
    ServerName eyrie.example.com

    SSLEngine on
    SSLCertificateFile    /etc/apache2/certs/eyrie.example.com.crt
    SSLCertificateKeyFile /etc/apache2/certs/eyrie.example.com.key

    ProxyPreserveHost On
    RequestHeader set X-Forwarded-Proto "https"
    # flushpackets: send the live updates on at once.
    ProxyPass        / http://127.0.0.1:8080/ flushpackets=on
    ProxyPassReverse / http://127.0.0.1:8080/
</VirtualHost>
```

`mod_proxy` adds `X-Forwarded-For` and `X-Forwarded-Host` itself. The
default `LimitRequestBody` (1 GiB) and timeout (60 s) are enough.

## HAProxy

```haproxy
defaults
    mode http
    timeout connect 5s
    timeout client  60s
    timeout server  60s

frontend web
    bind :80
    bind :443 ssl crt /etc/haproxy/certs/eyrie.example.com.pem
    http-request redirect scheme https unless { ssl_fc }
    http-request set-header X-Forwarded-Proto https
    http-request set-header X-Forwarded-Host %[req.hdr(host)]
    option forwardfor
    default_backend eyrie

backend eyrie
    option httpchk GET /healthz
    server eyrie 127.0.0.1:8080 check
```

Keep `timeout client` and `timeout server` above 25 s: they close a
connection that is quiet for that long, and the event stream is quiet
between keepalives. `/healthz` answers 200 while Eyrie's database
answers. HAProxy has no body limit.
