import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import {
  Avatar,
  Button,
  Dropdown,
  Input,
  Layout,
  Menu,
  Result,
  Space,
  Spin,
  Tag,
  Tooltip,
  Typography,
  App as AntdApp,
} from 'antd'
import {
  CloudServerOutlined,
  DeleteOutlined,
  FileSearchOutlined,
  FolderOpenOutlined,
  LockOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  SettingOutlined,
  TeamOutlined,
  UserOutlined,
} from '@ant-design/icons'
import type { MenuProps } from 'antd'
import {
  BrowserRouter,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router-dom'
import { api, ApiError, folderDownloadUrl } from './api'
import type { PublicSettings, SetupStatus, User } from './types'
import { ChangePasswordModal, LoginPage, SetupPage } from './pages/Auth'
import { FolderTree } from './components/FolderTree'
import { FileBrowser, type FileBrowserHandle } from './components/FileBrowser'
import { AdminUsers } from './pages/AdminUsers'
import { AdminRecycle } from './pages/AdminRecycle'
import { AdminAudit } from './pages/AdminAudit'
import { AdminSystem } from './pages/AdminSystem'
import { AdminSettings } from './pages/AdminSettings'
import { roleColors, roleLabels } from './labels'

const { Header, Sider, Content } = Layout
const { Text } = Typography

function BootScreen({ text = '正在加载…' }: { text?: string }) {
  return (
    <div className="boot-screen">
      <div className="boot-logo"><CloudServerOutlined /></div>
      <Spin />
      <Text type="secondary">{text}</Text>
    </div>
  )
}

export default function App() {
  const { message } = AntdApp.useApp()
  const [user, setUser] = useState<User | null>(null)
  const [settings, setSettings] = useState<PublicSettings | null>(null)
  const [setupStatus, setSetupStatus] = useState<SetupStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [bootError, setBootError] = useState('')

  const refreshMe = useCallback(async () => {
    const me = await api.me()
    setUser(me.user)
    setSettings(me.settings)
  }, [])

  const bootstrap = useCallback(async () => {
    setLoading(true)
    setBootError('')
    try {
      await refreshMe()
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        try {
          setSetupStatus(await api.setupStatus())
        } catch (setupErr) {
          setBootError(setupErr instanceof Error ? setupErr.message : '无法连接服务器')
        }
      } else {
        setBootError(err instanceof Error ? err.message : '无法连接服务器')
      }
    } finally {
      setLoading(false)
    }
  }, [refreshMe])

  useEffect(() => {
    void bootstrap()
  }, [bootstrap])

  useEffect(() => {
    document.title = settings?.site_name || '办公室文件中心'
  }, [settings?.site_name])

  useEffect(() => {
    function onUnauthorized() {
      setUser((current) => {
        if (current) {
          message.warning('登录状态已失效，请重新登录')
        }
        return null
      })
    }
    window.addEventListener('lfp-unauthorized', onUnauthorized)
    return () => window.removeEventListener('lfp-unauthorized', onUnauthorized)
  }, [message])

  async function logout() {
    try {
      await api.logout()
    } catch {
      // 本地状态无论如何都要清除
    }
    setUser(null)
    message.success('已退出登录')
  }

  if (loading) {
    return <BootScreen />
  }

  if (bootError) {
    return (
      <div className="boot-screen">
        <Result
          status="error"
          title="无法连接文件服务器"
          subTitle={bootError}
          extra={<Button type="primary" onClick={() => void bootstrap()}>重试</Button>}
        />
      </div>
    )
  }

  if (!user) {
    if (setupStatus?.needs_setup) {
      return (
        <SetupPage
          status={setupStatus}
          onSuccess={(nextUser, nextSettings) => {
            setUser(nextUser)
            setSettings(nextSettings)
          }}
        />
      )
    }
    return (
      <LoginPage
        siteName={settings?.site_name || setupStatus?.site_name || '办公室文件中心'}
        onSuccess={(nextUser, nextSettings) => {
          setUser(nextUser)
          setSettings(nextSettings)
        }}
      />
    )
  }

  if (user.must_change_password) {
    return (
      <div className="forced-page">
        <div className="forced-card">
          <div className="boot-logo"><LockOutlined /></div>
          <h2>请先设置新密码</h2>
          <p>管理员为你设置了初始密码。为了账号安全，修改后才能进入文件中心。</p>
          <Button type="text" icon={<LogoutOutlined />} onClick={() => void logout()}>退出登录</Button>
        </div>
        <ChangePasswordModal
          open
          forced
          onClose={() => undefined}
          onSuccess={() => void refreshMe()}
        />
      </div>
    )
  }

  return (
    <BrowserRouter>
      <Shell
        user={user}
        settings={settings!}
        refreshMe={refreshMe}
        onLogout={logout}
      />
    </BrowserRouter>
  )
}

function Shell({
  user,
  settings,
  refreshMe,
  onLogout,
}: {
  user: User
  settings: PublicSettings
  refreshMe: () => Promise<void>
  onLogout: () => Promise<void>
}) {
  const { message } = AntdApp.useApp()
  const navigate = useNavigate()
  const location = useLocation()
  const [folderId, setFolderId] = useState('')
  const [searchInput, setSearchInput] = useState('')
  const [searchQuery, setSearchQuery] = useState('')
  const [treeVersion, setTreeVersion] = useState(0)
  const [newFolderSignal, setNewFolderSignal] = useState(0)
  const [passwordOpen, setPasswordOpen] = useState(false)
  const browserRef = useRef<FileBrowserHandle>(null)

  const isAdminArea = location.pathname.startsWith('/admin')
  const isAdmin = user.role === 'admin'

  const openFolder = useCallback(
    (id: string) => {
      setFolderId(id)
      setSearchQuery('')
      setSearchInput('')
      if (location.pathname !== '/') navigate('/')
    },
    [location.pathname, navigate],
  )

  const bumpTree = useCallback(() => setTreeVersion((value) => value + 1), [])

  const adminMenu: MenuProps['items'] = [
    { key: '/admin/users', icon: <TeamOutlined />, label: '用户管理' },
    { key: '/admin/recycle', icon: <DeleteOutlined />, label: '回收站' },
    { key: '/admin/audit', icon: <FileSearchOutlined />, label: '操作日志' },
    { key: '/admin/system', icon: <SafetyCertificateOutlined />, label: '备份与系统' },
    { key: '/admin/settings', icon: <SettingOutlined />, label: '系统设置' },
  ]

  const userMenu: MenuProps['items'] = [
    { key: 'password', icon: <LockOutlined />, label: '修改密码' },
    ...(isAdmin ? [{ key: 'admin', icon: <SettingOutlined />, label: isAdminArea ? '返回文件' : '管理后台' }] : []),
    { type: 'divider' as const },
    { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true },
  ]

  return (
    <Layout className="app-shell">
      <Header className="app-header">
        <div className="header-left">
          <div className="brand" onClick={() => navigate('/')}>
            <span className="brand-mark"><CloudServerOutlined /></span>
            <span className="brand-name">{settings.site_name}</span>
          </div>
        </div>
        <div className="header-search">
          {!isAdminArea && (
            <Input.Search
              prefix={<SearchOutlined />}
              placeholder="搜索文件或文件夹名称"
              allowClear
              value={searchInput}
              onChange={(event) => {
                setSearchInput(event.target.value)
                if (!event.target.value) setSearchQuery('')
              }}
              onSearch={(value) => {
                setSearchQuery(value.trim())
                navigate('/')
              }}
            />
          )}
        </div>
        <div className="header-right">
          {isAdmin && (
            <Button
              type={isAdminArea ? 'default' : 'text'}
              icon={isAdminArea ? <FolderOpenOutlined /> : <SettingOutlined />}
              onClick={() => navigate(isAdminArea ? '/' : '/admin/users')}
            >
              {isAdminArea ? '返回文件' : '管理后台'}
            </Button>
          )}
          {isAdminArea && !isAdmin && <Button onClick={() => navigate('/')}>返回文件</Button>}
          <Dropdown
            trigger={['click']}
            menu={{
              items: userMenu,
              onClick: ({ key }) => {
                if (key === 'password') setPasswordOpen(true)
                if (key === 'admin') navigate(isAdminArea ? '/' : '/admin/users')
                if (key === 'logout') void onLogout()
              },
            }}
          >
            <button className="user-chip">
              <Avatar size={34} className="user-avatar">{user.display_name.slice(0, 1)}</Avatar>
              <span className="user-meta">
                <span className="user-name">{user.display_name}</span>
                <span className="user-role">{roleLabels[user.role]}</span>
              </span>
              <MenuFoldOutlined className="user-caret" rotate={90} />
            </button>
          </Dropdown>
        </div>
      </Header>

      <Layout className="app-body">
        <Sider width={305} theme="light" className="app-sider">
          {isAdminArea ? (
            <div className="admin-nav">
              <div className="folder-tree-title">管理后台</div>
              <Menu
                mode="inline"
                selectedKeys={[location.pathname]}
                items={adminMenu}
                onClick={({ key }) => navigate(key)}
              />
            </div>
          ) : (
            <FolderTree
              selected={folderId}
              version={treeVersion}
              canEdit={isAdmin || user.role === 'editor'}
              onSelect={openFolder}
              onNewProject={() => {
                openFolder('')
                setNewFolderSignal((value) => value + 1)
              }}
              onNodeAction={(action, node) => {
                switch (action) {
                  case 'download':
                    window.location.href = folderDownloadUrl(node.id)
                    break
                  case 'new-folder':
                    openFolder(node.id)
                    setNewFolderSignal((value) => value + 1)
                    break
                  case 'rename':
                    browserRef.current?.rename(node)
                    break
                  case 'move':
                    browserRef.current?.move(node)
                    break
                  case 'delete':
                    browserRef.current?.remove(node)
                    break
                }
              }}
              onAreaAction={(action) => {
                switch (action) {
                  case 'new-project':
                    openFolder('')
                    setNewFolderSignal((value) => value + 1)
                    break
                  case 'upload':
                    browserRef.current?.pickFiles()
                    break
                  case 'refresh':
                    browserRef.current?.refresh()
                    bumpTree()
                    break
                }
              }}
            />
          )}
          <div className="sider-foot">
            <SecurityHint />
          </div>
        </Sider>

        <Content className="app-content">
          <Routes>
            <Route
              path="/"
              element={
                <FileBrowser
                  ref={browserRef}
                  user={user}
                  settings={settings}
                  folderId={folderId}
                  searchQuery={searchQuery}
                  newFolderSignal={newFolderSignal}
                  onFolderChange={openFolder}
                  onClearSearch={() => {
                    setSearchQuery('')
                    setSearchInput('')
                  }}
                  onTreeChanged={bumpTree}
                />
              }
            />
            <Route path="/admin" element={isAdmin ? <Navigate to="/admin/users" replace /> : <Navigate to="/" replace />} />
            <Route
              path="/admin/users"
              element={isAdmin ? <AdminUsers currentUser={user} /> : <Navigate to="/" replace />}
            />
            <Route
              path="/admin/recycle"
              element={isAdmin ? <AdminRecycle settings={settings} /> : <Navigate to="/" replace />}
            />
            <Route path="/admin/audit" element={isAdmin ? <AdminAudit /> : <Navigate to="/" replace />} />
            <Route
              path="/admin/system"
              element={
                isAdmin ? (
                  <AdminSystem settings={settings} onOpenSettings={() => navigate('/admin/settings')} />
                ) : (
                  <Navigate to="/" replace />
                )
              }
            />
            <Route
              path="/admin/settings"
              element={
                isAdmin ? (
                  <AdminSettings
                    onSaved={() => {
                      void refreshMe().catch(() => message.error('刷新设置失败'))
                    }}
                  />
                ) : (
                  <Navigate to="/" replace />
                )
              }
            />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Content>
      </Layout>

      <ChangePasswordModal
        open={passwordOpen}
        forced={false}
        onClose={() => setPasswordOpen(false)}
        onSuccess={() => {
          setPasswordOpen(false)
          void refreshMe()
        }}
      />
    </Layout>
  )
}

function SecurityHint() {
  return (
    <div className="security-hint">
      <Tooltip title="文件保存在办公室主机；历史版本可在文件详情中查看。">
        <span><SafetyCertificateOutlined /> 数据保存在本机服务器</span>
      </Tooltip>
      <span className="sider-version">局域网内部使用</span>
    </div>
  )
}
