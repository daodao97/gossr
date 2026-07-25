import definition from './app'
import { bootstrapClient } from '@daodao97/gossr-vue/client'

void bootstrapClient(definition).catch((error) => {
  console.error('[example] bootstrap failed', error)
  document.querySelector<HTMLElement>('#app')?.setAttribute('data-bootstrap-failed', 'true')
})
