import './styles/tokens.css'
import { mount } from 'svelte'
import App from './App.svelte'
import { blockMouseNavigation } from './lib/mousenav'

blockMouseNavigation()

const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app
