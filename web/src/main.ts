import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'

// These are the actual upstream art-design-pro style entry points pulled into
// third_party. Keeping the imports here makes upstream provenance auditable.
import '../../third_party/art-design-pro/src/assets/styles/index.scss'
import '../../third_party/art-design-pro/src/views/auth/login/style.css'
import './workspace.scss'
import App from './App.vue'

createApp(App).use(ElementPlus).mount('#app')
