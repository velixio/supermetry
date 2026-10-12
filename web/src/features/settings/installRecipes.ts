// Install recipes: one copy-ready snippet per framework, generated from the
// site's real tracker URL so nothing has to be edited by hand.

export type RecipeInput = {
  publicId: string
  /** Absolute tracker URL, e.g. https://stats.example.com/js/script.js */
  scriptUrl: string
}

export type RecipeLanguage = 'html' | 'tsx' | 'ts' | 'js' | 'php' | 'liquid' | 'sh' | 'python' | 'go'

export type Recipe = {
  id: string
  label: string
  group: 'browser' | 'server'
  /** File or place the snippet goes. */
  where: string
  note?: string
  language: RecipeLanguage
  code: string
}

/** Pulls the tracker URL and site id out of the server-generated script tag. */
export function parseSnippet(snippet: string | undefined, fallbackPublicId: string, fallbackOrigin: string): RecipeInput {
  const src = snippet?.match(/\bsrc="([^"]+)"/)?.[1]
  const site = snippet?.match(/\bdata-site="([^"]+)"/)?.[1]
  return {
    publicId: site ?? fallbackPublicId,
    scriptUrl: src ? new URL(src, fallbackOrigin).toString() : `${fallbackOrigin}/js/script.js`,
  }
}

export function eventsUrl(scriptUrl: string): string {
  return `${new URL(scriptUrl).origin}/api/public/v1/events`
}

const q = (value: string) => JSON.stringify(value)

// JSX attribute strings cannot contain escapes, so unusual values go through an
// expression container; ordinary ids and URLs keep the familiar quoted form.
const jsxAttr = (name: string, value: string) => (/^[A-Za-z0-9._:/?=&%#~+-]+$/.test(value) ? `${name}="${value}"` : `${name}={${q(value)}}`)

export function buildRecipes({ publicId, scriptUrl }: RecipeInput): Recipe[] {
  const tag = `<script defer data-site="${publicId}" src="${scriptUrl}"></script>`
  const events = eventsUrl(scriptUrl)
  const headNote = 'Paste it inside <head> so it loads on every page.'
  return [
    { id: 'html', label: 'HTML', group: 'browser', where: 'Every page, inside <head>', language: 'html', code: tag },
    {
      id: 'react', label: 'React (Vite, CRA)', group: 'browser', where: 'index.html', language: 'html',
      note: 'Single-page route changes are tracked automatically; add the tag once to the HTML shell.', code: tag,
    },
    {
      id: 'nextjs', label: 'Next.js', group: 'browser', where: 'app/layout.tsx', language: 'tsx',
      code: `import Script from 'next/script'

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        {children}
        <Script defer ${jsxAttr('data-site', publicId)} ${jsxAttr('src', scriptUrl)} strategy="afterInteractive" />
      </body>
    </html>
  )
}`,
    },
    { id: 'vue', label: 'Vue (Vite)', group: 'browser', where: 'index.html', language: 'html', note: headNote, code: tag },
    {
      id: 'nuxt', label: 'Nuxt', group: 'browser', where: 'nuxt.config.ts', language: 'ts',
      code: `export default defineNuxtConfig({
  app: {
    head: {
      script: [{ src: ${q(scriptUrl)}, defer: true, 'data-site': ${q(publicId)} }],
    },
  },
})`,
    },
    { id: 'sveltekit', label: 'SvelteKit', group: 'browser', where: 'src/app.html', language: 'html', note: headNote, code: tag },
    {
      id: 'astro', label: 'Astro', group: 'browser', where: 'Your layout, inside <head>', language: 'html',
      note: 'is:inline stops Astro from bundling the tag.', code: `<script is:inline defer data-site="${publicId}" src="${scriptUrl}"></script>`,
    },
    {
      id: 'remix', label: 'Remix', group: 'browser', where: 'app/root.tsx', language: 'tsx',
      code: `export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <meta charSet="utf-8" />
        <script defer ${jsxAttr('data-site', publicId)} ${jsxAttr('src', scriptUrl)} />
      </head>
      <body>{children}</body>
    </html>
  )
}`,
    },
    { id: 'angular', label: 'Angular', group: 'browser', where: 'src/index.html', language: 'html', note: headNote, code: tag },
    {
      id: 'wordpress', label: 'WordPress', group: 'browser', where: "Your theme's functions.php (or a header-injection plugin)", language: 'php',
      code: `add_action('wp_head', function () {
    echo '<script defer data-site="${publicId}" src="${scriptUrl}"></script>';
});`,
    },
    {
      id: 'shopify', label: 'Shopify', group: 'browser', where: 'Online Store → Themes → Edit code → layout/theme.liquid', language: 'liquid',
      note: 'Paste it just before </head>.', code: tag,
    },
    {
      id: 'gtm', label: 'Google Tag Manager', group: 'browser', where: 'New tag → Custom HTML, trigger: All Pages', language: 'html',
      code: `<script>
  (function () {
    var s = document.createElement('script');
    s.defer = true;
    s.src = ${q(scriptUrl)};
    s.setAttribute('data-site', ${q(publicId)});
    document.head.appendChild(s);
  })();
</script>`,
    },
    {
      id: 'curl', label: 'curl', group: 'server', where: 'Any shell or backend job', language: 'sh',
      note: 'Forward the visitor user agent and IP so the event joins the right session.',
      code: `curl -X POST ${events} \\
  -H "Content-Type: application/json" \\
  -H "User-Agent: $ORIGINAL_USER_AGENT" \\
  -H "X-Forwarded-For: $ORIGINAL_CLIENT_IP" \\
  -d '{"site":"${publicId}","name":"Signup","url":"https://example.com/signup","props":{"plan":"pro"}}'`,
    },
    {
      id: 'node', label: 'Node.js', group: 'server', where: 'Server code (Node 18+)', language: 'js',
      code: `async function track(name, req, props = {}) {
  await fetch(${q(events)}, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'User-Agent': req.headers['user-agent'] ?? '',
      'X-Forwarded-For': req.ip,
    },
    body: JSON.stringify({ site: ${q(publicId)}, name, url: req.protocol + '://' + req.get('host') + req.originalUrl, props }),
  })
}`,
    },
    {
      id: 'python', label: 'Python', group: 'server', where: 'Server code (requests)', language: 'python',
      code: `import requests

def track(name, user_agent, client_ip, url, props=None):
    requests.post(
        ${q(events)},
        headers={"User-Agent": user_agent, "X-Forwarded-For": client_ip},
        json={"site": ${q(publicId)}, "name": name, "url": url, "props": props or {}},
        timeout=3,
    )`,
    },
    {
      id: 'go', label: 'Go', group: 'server', where: 'Server code (net/http)', language: 'go',
      code: `func track(name string, r *http.Request, props map[string]string) error {
	body, err := json.Marshal(map[string]any{
		"site": ${q(publicId)}, "name": name, "url": "https://" + r.Host + r.URL.RequestURI(), "props": props,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, ${q(events)}, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", r.UserAgent())
	req.Header.Set("X-Forwarded-For", r.RemoteAddr)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}`,
    },
  ]
}
