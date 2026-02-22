import { createSSRApp } from 'vue'
import App from './App.vue'

export interface ExamplePayload {
  message?: string
  path?: string
  query?: string
  generatedAt?: string
}

export function makeApp(url: string, payload: ExamplePayload) {
  return createSSRApp(App, { url, payload })
}
