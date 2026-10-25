import { App as AntApp, ConfigProvider } from 'antd'
import { ChatPage } from './chat/ChatPage'

export default function App() {
  return (
    <ConfigProvider theme={{ token: { borderRadius: 8 } }}>
      <AntApp>
        <ChatPage />
      </AntApp>
    </ConfigProvider>
  )
}
