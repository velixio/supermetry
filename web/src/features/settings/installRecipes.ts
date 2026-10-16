// Install recipes: one copy-ready snippet per framework, generated from the
// site's real tracker URL so nothing has to be edited by hand. Frameworks with an
// official @supermetry package use it with the site id and host filled in.

export type RecipeInput = {
  publicId: string
  /** Absolute tracker URL, e.g. https://stats.example.com/js/script.js */
  scriptUrl: string
}

export type RecipeLanguage = 'html' | 'tsx' | 'ts' | 'js' | 'php' | 'liquid' | 'sh' | 'python' | 'go'

export type Recipe = {
  id: string
  label: string
  group: 'browser' | 'server' | 'mobile'
  /** Package install command, for recipes built on an @supermetry package. */
  install?: string
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

/** The Supermetry origin the npm packages take as `host`. */
export function trackerHost(scriptUrl: string): string {
  return new URL(scriptUrl).origin
}

export function eventsUrl(scriptUrl: string): string {
  return `${trackerHost(scriptUrl)}/api/public/v1/events`
}

const q = (value: string) => JSON.stringify(value)

// JSX attribute strings cannot contain escapes, so unusual values go through an
// expression container; ordinary ids and URLs keep the familiar quoted form.
const jsxAttr = (name: string, value: string) => (/^[A-Za-z0-9._:/?=&%#~+-]+$/.test(value) ? `${name}="${value}"` : `${name}={${q(value)}}`)

export function buildRecipes({ publicId, scriptUrl }: RecipeInput): Recipe[] {
  const tag = `<script defer data-site="${publicId}" src="${scriptUrl}"></script>`
  const events = eventsUrl(scriptUrl)
  const host = trackerHost(scriptUrl)
  const props = `${jsxAttr('site', publicId)} ${jsxAttr('host', host)}`
  const options = `site: ${q(publicId)}, host: ${q(host)}`
  const headNote = 'Paste it inside <head> so it loads on every page.'
  return [
    { id: 'html', label: 'HTML', group: 'browser', where: 'Every page, inside <head>', language: 'html', code: tag },
    {
      id: 'npm', label: 'JavaScript (npm)', group: 'browser', install: 'npm install @supermetry/tracker', where: 'Your app entry point', language: 'ts',
      note: 'Loads the tracker once; call track() anywhere afterwards.',
      code: `import { init } from '@supermetry/tracker'

const supermetry = init({ ${options} })

supermetry.track('Signup', { plan: 'pro' })`,
    },
    {
      id: 'react', label: 'React', group: 'browser', install: 'npm install @supermetry/react', where: 'src/main.tsx', language: 'tsx',
      note: 'Route changes are tracked automatically. Call useSupermetry() in any component to send events.',
      code: `import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { SupermetryProvider } from '@supermetry/react'

import App from './App'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <SupermetryProvider ${props}>
      <App />
    </SupermetryProvider>
  </StrictMode>,
)`,
    },
    {
      id: 'nextjs', label: 'Next.js', group: 'browser', install: 'npm install @supermetry/nextjs', where: 'app/layout.tsx', language: 'tsx',
      code: `import { Supermetry } from '@supermetry/nextjs'

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Supermetry ${props} />
        {children}
      </body>
    </html>
  )
}`,
    },
    {
      id: 'vue', label: 'Vue', group: 'browser', install: 'npm install @supermetry/vue', where: 'src/main.ts', language: 'ts',
      note: 'Passing the router tracks every route change.',
      code: `import { createApp } from 'vue'
import { Supermetry } from '@supermetry/vue'

import App from './App.vue'
import router from './router'

createApp(App)
  .use(router)
  .use(Supermetry, { ${options}, router })
  .mount('#app')`,
    },
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
      id: 'astro', label: 'Astro', group: 'browser', install: 'npm install @supermetry/astro', where: 'src/layouts/Layout.astro', language: 'html',
      note: 'With View Transitions, add the viewTransitions prop.',
      code: `---
import Supermetry from '@supermetry/astro'
---

<html lang="en">
  <head>
    <Supermetry site=${q(publicId)} host=${q(host)} />
  </head>
  <body><slot /></body>
</html>`,
    },
    {
      id: 'remix', label: 'Remix', group: 'browser', install: 'npm install @supermetry/remix', where: 'app/root.tsx', language: 'tsx',
      code: `import { Links, Meta, Outlet, Scripts } from '@remix-run/react'
import { Supermetry } from '@supermetry/remix'

export default function App() {
  return (
    <html lang="en">
      <head>
        <Meta />
        <Links />
      </head>
      <body>
        <Outlet />
        <Supermetry ${props} />
        <Scripts />
      </body>
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
      id: 'node', label: 'Node.js', group: 'server', install: 'npm install @supermetry/node', where: 'Server code (Node 18+)', language: 'js',
      note: 'Forward the visitor user agent and IP so the event joins the right session.',
      code: `import { Supermetry } from '@supermetry/node'

const supermetry = new Supermetry({ ${options} })

supermetry.track('Signup', {
  url: 'https://example.com/signup',
  props: { plan: 'pro' },
  userAgent: req.headers['user-agent'],
  ip: req.ip,
})`,
    },
    {
      id: 'express', label: 'Express', group: 'server', install: 'npm install @supermetry/express', where: 'Your Express app', language: 'ts',
      code: `import express from 'express'
import { supermetry } from '@supermetry/express'

const app = express()
app.set('trust proxy', true)
app.use(supermetry({ ${options}, trackPageviews: true }))

app.post('/api/signup', (req, res) => {
  req.supermetry.track('Signup', { props: { plan: 'pro' } })
  res.json({ ok: true })
})`,
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
    {
      id: 'react-native', label: 'React Native', group: 'mobile', install: 'npm install @supermetry/react-native @react-native-async-storage/async-storage',
      where: 'App.tsx', language: 'tsx',
      note: 'appUrl is the base URL screens are reported under; its host must be an allowed host of this site.',
      code: `import AsyncStorage from '@react-native-async-storage/async-storage'
import { NavigationContainer, useNavigationContainerRef } from '@react-navigation/native'
import { SupermetryProvider, createSupermetry, trackNavigation } from '@supermetry/react-native'

const supermetry = createSupermetry({
  ${options},
  appUrl: 'https://app.example.com',
  storage: AsyncStorage,
})

export default function App() {
  const navigationRef = useNavigationContainerRef()
  return (
    <SupermetryProvider client={supermetry}>
      <NavigationContainer ref={navigationRef} {...trackNavigation(supermetry, navigationRef)}>
        {/* screens */}
      </NavigationContainer>
    </SupermetryProvider>
  )
}`,
    },
  ]
}

/** A prompt to paste into an AI coding assistant, with this site's real values. */
export function aiInstallPrompt({ publicId, scriptUrl }: RecipeInput): string {
  const host = trackerHost(scriptUrl)
  return `Add Supermetry analytics to this project.

Site ID: ${publicId}
Host: ${host}

1. Detect the framework and use its official package, passing site and host:
   - React: @supermetry/react (<SupermetryProvider site host> around the app)
   - Next.js App Router: @supermetry/nextjs (<Supermetry site host /> in app/layout.tsx)
   - Vue 3: @supermetry/vue (app.use(Supermetry, { site, host, router }))
   - Astro: @supermetry/astro (<Supermetry site host /> in the layout <head>)
   - Remix / React Router 7: @supermetry/remix (<Supermetry site host /> in app/root.tsx)
   - Other browser JavaScript: @supermetry/tracker (init({ site, host }))
   - Node.js server: @supermetry/node; Express: @supermetry/express
   - React Native: @supermetry/react-native
   Install it with the project's package manager.
2. If no package fits (plain HTML, WordPress, Shopify, other stacks), add this tag to every page's <head>:
   <script defer data-site="${publicId}" src="${scriptUrl}"></script>
3. Server-side events: POST JSON to ${host}/api/public/v1/events and forward the visitor's User-Agent and X-Forwarded-For.
4. Load the tracker once, on every page, so pageviews are not counted twice.
5. Package docs: https://github.com/velixio/supermetry/tree/main/sdk

When done, tell me which files you changed.`
}
