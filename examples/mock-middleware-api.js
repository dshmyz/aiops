import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = dirname(fileURLToPath(import.meta.url));
const openapi = readFileSync(join(root, 'middleware-openapi.yaml'), 'utf8');

// In-memory topic retention state so verify-after-write can observe the change.
const topicRetention = new Map();

const routes = [
  {
    pattern: /^\/api\/minio\/([^/]+)\/buckets\/([^/]+)\/capacity$/,
    handler: ([cluster, bucket]) => ({
      status: bucket === 'archive' ? 'warning' : 'ok',
      data: {
        cluster,
        bucket,
        usage_pct: bucket === 'archive' ? 77 : 42,
        used_gb: bucket === 'archive' ? 154 : 84,
        limit_gb: 200,
      },
    }),
  },
  {
    pattern: /^\/api\/glusterfs\/([^/]+)\/volumes\/([^/]+)\/status$/,
    handler: ([cluster, volume]) => ({
      status: 'ok',
      data: {
        cluster,
        volume,
        online_bricks: 6,
        offline_bricks: 0,
        heal_backlog: 3,
      },
    }),
  },
  {
    pattern: /^\/api\/kafka\/([^/]+)\/consumer-groups\/([^/]+)\/lag$/,
    handler: ([cluster, group]) => ({
      status: group === 'payments' ? 'critical' : 'ok',
      data: {
        cluster,
        group,
        lag: group === 'payments' ? 1288 : 12,
        members: 3,
      },
    }),
  },
  {
    pattern: /^\/api\/kafka\/([^/]+)\/topics\/([^/]+)\/retention$/,
    handler: ([cluster, topic]) => {
      const key = `${cluster}/${topic}`;
      const hours = topicRetention.has(key) ? topicRetention.get(key) : 24;
      return {
        status: 'ok',
        data: {
          cluster,
          topic,
          retention_hours: hours,
        },
      };
    },
  },
];

const server = createServer((request, response) => {
  const url = new URL(request.url ?? '/', 'http://127.0.0.1:19090');
  if (request.method === 'GET' && url.pathname === '/v3/api-docs') {
    write(response, 200, openapi, 'application/yaml');
    return;
  }
  if (request.method === 'POST' && /^\/api\/kafka\/([^/]+)\/topics\/([^/]+)\/retention$/.test(url.pathname)) {
    const match = url.pathname.match(/^\/api\/kafka\/([^/]+)\/topics\/([^/]+)\/retention$/);
    const cluster = decodeURIComponent(match[1]);
    const topic = decodeURIComponent(match[2]);
    let body = '';
    request.on('data', (chunk) => { body += chunk; });
    request.on('end', () => {
      let retentionHours = null;
      try {
        const parsed = JSON.parse(body || '{}');
        retentionHours = parsed.retention_hours ?? null;
      } catch {
        retentionHours = null;
      }
      if (retentionHours != null) {
        topicRetention.set(`${cluster}/${topic}`, Number(retentionHours));
      }
      writeJSON(response, 200, { status: 'accepted', data: { cluster, topic, retention_hours: retentionHours, message: 'retention update accepted' } });
    });
    return;
  }
  for (const route of routes) {
    const match = url.pathname.match(route.pattern);
    if (request.method === 'GET' && match) {
      writeJSON(response, 200, route.handler(match.slice(1).map(decodeURIComponent)));
      return;
    }
  }
  writeJSON(response, 404, { error: 'not found' });
});

server.listen(19090, '127.0.0.1', () => {
  console.log('mock middleware API listening on http://127.0.0.1:19090');
});

function writeJSON(response, status, body) {
  write(response, status, JSON.stringify(body, null, 2), 'application/json');
}

function write(response, status, body, contentType) {
  response.writeHead(status, {
    'Access-Control-Allow-Origin': '*',
    'Content-Type': contentType,
  });
  response.end(body);
}

