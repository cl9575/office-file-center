import React from 'react'
import ReactDOM from 'react-dom/client'
import { App as AntApp, ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import dayjs from 'dayjs'
import 'dayjs/locale/zh-cn'
import 'antd/dist/reset.css'
import './styles.css'
import App from './App'

dayjs.locale('zh-cn')

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        token: {
          colorPrimary: '#3a79bc',
          colorInfo: '#3a79bc',
          colorInfoBg: '#edf4fd',
          colorInfoBorder: '#d8e6f8',
          colorInfoText: '#245a96',
          colorLink: '#3a79bc',
          colorTextBase: '#171a1e',
          colorText: '#171a1e',
          colorTextSecondary: '#4f5761',
          colorTextTertiary: '#6b7480',
          colorTextQuaternary: '#9aa2ac',
          colorTextPlaceholder: '#8b939d',
          colorBorder: '#e6e8eb',
          colorBorderSecondary: '#ebedf0',
          borderRadius: 9,
          fontFamily:
            '"Segoe UI", "Microsoft YaHei", "PingFang SC", -apple-system, BlinkMacSystemFont, sans-serif',
          colorBgLayout: '#f6f8fc',
        },
        components: {
          Layout: { headerBg: '#ffffff', siderBg: '#f9fafc' },
          Table: { headerBg: '#f2f5f9', headerColor: '#4a5562' },
          Button: { controlHeight: 36 },
        },
      }}
    >
      <AntApp>
        <App />
      </AntApp>
    </ConfigProvider>
  </React.StrictMode>,
)
