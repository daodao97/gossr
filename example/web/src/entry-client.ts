import { makeApp, type ExamplePayload } from './main'

declare global {
  interface Window {
    __SSR_DATA__?: ExamplePayload
  }
}

const ssrPayload = window.__SSR_DATA__
const hadInitialSsrPayload = !!ssrPayload && Object.keys(ssrPayload).length > 0
const initialState: ExamplePayload = ssrPayload ?? {}

void bootstrap()

async function bootstrap() {
  const path = `${window.location.pathname}${window.location.search}`

  if (!hadInitialSsrPayload) {
    try {
      const initialData = await fetchSsrData(path)
      Object.assign(initialState, initialData)
    }
    catch (error) {
      console.error('Failed to fetch initial SSR data', error)
    }
  }

  const app = makeApp(path, initialState)
  app.mount('#app', true)
  delete window.__SSR_DATA__
}

async function fetchSsrData(path: string): Promise<ExamplePayload> {
  const url = new URL(path, window.location.origin)
  const endpoint = `/__ssr_fetch${url.pathname}${url.search}`
  const response = await fetch(endpoint, {
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      'X-SSR-Fetch': '1',
    },
  })

  if (!response.ok)
    throw new Error(`Request failed with status ${response.status}`)

  const data = await response.json()
  if (data && typeof data === 'object')
    return data as ExamplePayload

  return {}
}
