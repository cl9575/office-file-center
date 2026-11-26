import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import {
  App,
  Breadcrumb,
  Button,
  Dropdown,
  Empty,
  Input,
  Modal,
  Space,
  Spin,
  Table,
  Tag,
  Tooltip,
  TreeSelect,
  Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { MenuProps } from 'antd'
import type { BreadcrumbProps } from 'antd'
import {
  ArrowLeftOutlined,
  CopyOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  EyeOutlined,
  AudioOutlined,
  CodeOutlined,
  FileExcelOutlined,
  FileImageOutlined,
  FileOutlined,
  FilePdfOutlined,
  FilePptOutlined,
  FileTextOutlined,
  FileWordOutlined,
  FileZipOutlined,
  FolderAddOutlined,
  FolderFilled,
  HistoryOutlined,
  InboxOutlined,
  MoreOutlined,
  ReloadOutlined,
  SearchOutlined,
  SwapOutlined,
  UploadOutlined,
  VideoCameraOutlined,
} from '@ant-design/icons'
import { api, ApiError, downloadUrl, folderDownloadUrl, formatBytes, previewUrl } from '../api'
import type { Breadcrumb as Crumb, NodeItem, NodeSpace, PublicSettings, UploadResult, User } from '../types'
import { fmtTime } from '../labels'
import { previewKindForName } from '../preview'
import { Uploader, type UploaderHandle } from './Uploader'
import { DetailsDrawer } from './DetailsDrawer'
import { ContextMenu, type ContextMenuState } from './ContextMenu'
import { PreviewModal } from './PreviewModal'

const { Text } = Typography

interface MoveTreeNode {
  title: string
  fullPath: string
  value: string
  selectable?: boolean
  children: MoveTreeNode[]
}

function movePathLabel(path: string | undefined, space: NodeSpace) {
  const parts = (path || '').split('/').filter(Boolean)
  if (space === 'secure') {
    return parts.length ? parts.join(' / ') : '权限区'
  }
  return parts.length ? `全部项目 / ${parts.join(' / ')}` : '全部项目'
}

function moveExpandedKeys(folders: NodeItem[], parentId: string, rootKey: string) {
  const byId = new Map(folders.map((folder) => [folder.id, folder]))
  const chain: string[] = []
  const seen = new Set<string>()
  let current = parentId
  while (current && !seen.has(current)) {
    seen.add(current)
    chain.push(current)
    current = byId.get(current)?.parent_id || ''
  }
  return [rootKey, ...chain.reverse()]
}

export interface FileBrowserHandle {
  rename: (node: NodeItem) => void
  move: (node: NodeItem) => void
  remove: (node: NodeItem) => void
  pickFiles: () => void
  refresh: () => void
}

const wordExts = new Set(['doc', 'docx', 'wps'])
const excelExts = new Set(['xls', 'xlsx', 'xlsm', 'csv', 'et'])
const pptExts = new Set(['ppt', 'pptx', 'dps'])
const archiveExts = new Set(['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz'])
const imageExts = new Set(['png', 'jpg', 'jpeg', 'gif', 'bmp', 'webp', 'svg', 'ico', 'tif', 'tiff', 'heic'])
const videoExts = new Set(['mp4', 'avi', 'mkv', 'mov', 'wmv', 'flv', 'webm', 'mpg', 'mpeg', 'm4v'])
const audioExts = new Set(['mp3', 'wav', 'flac', 'aac', 'ogg', 'wma', 'm4a', 'ape'])
const textExts = new Set(['txt', 'md', 'log', 'rtf', 'ini', 'cfg', 'conf'])
const codeExts = new Set([
  'c', 'h', 'cpp', 'hpp', 'cxx', 'py', 'js', 'jsx', 'ts', 'tsx', 'json', 'xml', 'yml', 'yaml',
  'html', 'htm', 'css', 'scss', 'sql', 'sh', 'bat', 'ps1', 'go', 'java', 'cs', 'php', 'rb', 'rs', 'vue',
])

function splitRenameName(name: string, kind: NodeItem['kind']) {
  if (kind !== 'file') return { base: name, ext: '' }
  const dot = name.lastIndexOf('.')
  if (dot <= 0) return { base: name, ext: '' }
  return { base: name.slice(0, dot), ext: name.slice(dot) }
}

function fileIcon(name: string) {
  const dot = name.lastIndexOf('.')
  const ext = dot > 0 ? name.slice(dot + 1).toLowerCase() : ''
  const style = { fontSize: 19 }
  if (wordExts.has(ext)) return <FileWordOutlined style={{ ...style, color: '#5f7fb0' }} />
  if (excelExts.has(ext)) return <FileExcelOutlined style={{ ...style, color: '#5f8f6d' }} />
  if (pptExts.has(ext)) return <FilePptOutlined style={{ ...style, color: '#bd7d5e' }} />
  if (ext === 'pdf') return <FilePdfOutlined style={{ ...style, color: '#b56b64' }} />
  if (archiveExts.has(ext)) return <FileZipOutlined style={{ ...style, color: '#a98b5f' }} />
  if (imageExts.has(ext)) return <FileImageOutlined style={{ ...style, color: '#5f988b' }} />
  if (videoExts.has(ext)) return <VideoCameraOutlined style={{ ...style, color: '#7d6fa3' }} />
  if (audioExts.has(ext)) return <AudioOutlined style={{ ...style, color: '#a26f92' }} />
  if (textExts.has(ext)) return <FileTextOutlined style={{ ...style, color: '#808b99' }} />
  if (codeExts.has(ext)) return <CodeOutlined style={{ ...style, color: '#5289a0' }} />
  return <FileOutlined style={{ ...style, color: '#97a0ab' }} />
}

export const FileBrowser = forwardRef<FileBrowserHandle, {
  user: User
  settings: PublicSettings
  folderId: string
  searchQuery: string
  newFolderSignal: number
  onFolderChange: (id: string) => void
  onClearSearch: () => void
  onTreeChanged: () => void
}>(function FileBrowser({
  user,
  settings,
  folderId,
  searchQuery,
  newFolderSignal,
  onFolderChange,
  onClearSearch,
  onTreeChanged,
}, ref) {
  const { message, modal } = App.useApp()
  const [items, setItems] = useState<NodeItem[]>([])
  const [crumbs, setCrumbs] = useState<Crumb[]>([{ id: '', name: '全部项目', space: 'main' }])
  const [currentSpace, setCurrentSpace] = useState<NodeSpace>('main')
  const [loading, setLoading] = useState(true)
  const [drawerNode, setDrawerNode] = useState<NodeItem | null>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [dragOver, setDragOver] = useState(false)
  const [newFolderOpen, setNewFolderOpen] = useState(false)
  const [newFolderName, setNewFolderName] = useState('')
  const [renameTarget, setRenameTarget] = useState<NodeItem | null>(null)
  const [renameName, setRenameName] = useState('')
  const [renameExt, setRenameExt] = useState('')
  const [moveTarget, setMoveTarget] = useState<NodeItem | null>(null)
  const [moveParent, setMoveParent] = useState('')
  const [moveFolders, setMoveFolders] = useState<NodeItem[]>([])
  const [moveFoldersReady, setMoveFoldersReady] = useState(false)
  const [saving, setSaving] = useState(false)
  const [previewNode, setPreviewNode] = useState<NodeItem | null>(null)
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null)
  const uploaderRef = useRef<UploaderHandle>(null)
  const dragDepth = useRef(0)
  const handledNewFolder = useRef(newFolderSignal)
  const refreshTimer = useRef<number | null>(null)

  const canEdit = user.role === 'admin' || user.role === 'operator' || user.role === 'editor'
  const canUploadSecure = user.role === 'admin' || user.role === 'operator'
  const canWriteHere = currentSpace === 'secure' ? canUploadSecure : canEdit
  const searching = !!searchQuery.trim()

  const load = useCallback(async () => {
    setLoading(true)
    try {
      if (searching) {
        const list = await api.listNodes('', searchQuery.trim())
        setItems(list)
        setCrumbs([{ id: '', name: '全部项目', space: 'main' }])
      } else {
        const [list, trail] = await Promise.all([
          api.listNodes(folderId, ''),
          folderId
            ? api.breadcrumbs(folderId)
            : Promise.resolve([{ id: '', name: '全部项目', space: 'main' as NodeSpace }]),
        ])
        setItems(list)
        setCrumbs(trail)
        setCurrentSpace(trail[0]?.space || 'main')
      }
    } catch (err) {
      if (err instanceof ApiError) {
        message.error(err.message)
        if (err.status === 404 && folderId) onFolderChange('')
      }
    } finally {
      setLoading(false)
    }
  }, [folderId, searching, searchQuery, message, onFolderChange])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (newFolderSignal === handledNewFolder.current) return
    handledNewFolder.current = newFolderSignal
    setNewFolderOpen(true)
  }, [newFolderSignal])

  function openNode(node: NodeItem) {
    if (node.kind === 'folder') {
      onFolderChange(node.id)
      return
    }
    setDrawerNode(node)
    setDrawerOpen(true)
  }

  function openPreview(node: NodeItem) {
    const kind = previewKindForName(node.name)
    if (node.kind !== 'file' || !kind) return
    if (kind === 'pdf') {
      const tab = window.open(previewUrl(node.id), '_blank')
      if (!tab) {
        message.warning('浏览器拦截了新标签页，请允许后重试')
        return
      }
      tab.opener = null
      return
    }
    setPreviewNode(node)
  }

  async function createFolder() {
    const atRoot = !folderId
    const name = newFolderName.trim()
    if (!name) {
      message.error(atRoot ? '请输入项目名称' : '请输入文件夹名称')
      return
    }
    setSaving(true)
    try {
      await api.createFolder(folderId, name)
      message.success(atRoot ? '项目已创建' : '文件夹已创建')
      setNewFolderOpen(false)
      setNewFolderName('')
      await load()
      onTreeChanged()
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function renameNode() {
    if (!renameTarget) return
    const base = renameName.trim()
    if (!base) {
      message.error('名称不能为空')
      return
    }
    const name = base + renameExt
    if (name === renameTarget.name) {
      setRenameTarget(null)
      return
    }
    setSaving(true)
    try {
      await api.patchNode(renameTarget.id, { name })
      message.success('名称已修改')
      setRenameTarget(null)
      await load()
      onTreeChanged()
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  function openRename(node: NodeItem) {
    const { base, ext } = splitRenameName(node.name, node.kind)
    setRenameTarget(node)
    setRenameName(base)
    setRenameExt(ext)
  }

  function openMove(node: NodeItem) {
    setMoveTarget(node)
    setMoveParent(node.parent_id || "__root__")
    setMoveFolders([])
    setMoveFoldersReady(false)
    api
      .folderTree()
      .then((folders) => {
        setMoveFolders(folders)
        setMoveFoldersReady(true)
      })
      .catch((err) => {
        message.error(err instanceof Error ? err.message : '无法读取目录')
        setMoveFoldersReady(true)
      })
  }

  async function moveNode() {
    if (!moveTarget) return
    setSaving(true)
    try {
      await api.patchNode(moveTarget.id, { parent_id: moveParent === "__root__" ? "" : moveParent })
      message.success('已移动')
      setMoveTarget(null)
      await load()
      onTreeChanged()
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  function confirmDelete(node: NodeItem) {
    modal.confirm({
      title: `将「${node.name}」移入回收站？`,
      content:
        node.space === 'secure'
          ? '项目及其历史版本会进入回收站，仅主机账号可恢复。'
          : node.kind === 'folder'
            ? '文件夹内的所有子目录、文件和历史版本会一并进入回收站，可由管理员恢复。'
            : '文件及其历史版本会进入回收站，可由管理员恢复。',
      okText: '移入回收站',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          const result = await api.deleteNode(node.id)
          message.success(`已移入回收站（${result.deleted} 项）`)
          await load()
          onTreeChanged()
        } catch (err) {
          if (err instanceof Error) message.error(err.message)
        }
      },
    })
  }

  function handleUploaded(_result: UploadResult) {
    if (refreshTimer.current) window.clearTimeout(refreshTimer.current)
    refreshTimer.current = window.setTimeout(() => {
      refreshTimer.current = null
      void load()
      onTreeChanged()
    }, 400)
  }

  useImperativeHandle(ref, () => ({
    rename: openRename,
    move: openMove,
    remove: confirmDelete,
    pickFiles: () => uploaderRef.current?.pickFiles(),
    refresh: () => void load(),
  }))

  function rowMenuItems(row: NodeItem): MenuProps['items'] {
    const items: MenuProps['items'] = [
      { key: 'copy-name', label: '复制文件名', icon: <CopyOutlined /> },
      { type: 'divider' },
    ]
    if (row.kind === 'file') {
      if (previewKindForName(row.name)) {
        items.push({ key: 'preview', label: '预览', icon: <EyeOutlined /> })
      }
      items.push(
        { key: 'download', label: '下载当前版本', icon: <DownloadOutlined /> },
        { key: 'history', label: '历史版本与记录', icon: <HistoryOutlined /> },
      )
    } else {
      items.push(
        { key: 'download-folder', label: '下载文件夹（ZIP）', icon: <DownloadOutlined /> },
      )
    }
    const rowCanManage = row.space === 'secure' ? !!row.can_manage : canEdit
    const rowCanDelete = row.space === 'secure' ? !!row.can_delete : canEdit
    if (rowCanManage || rowCanDelete) {
      items.push({ type: 'divider' })
      if (rowCanManage) {
        items.push(
          { key: 'rename', label: '重命名', icon: <EditOutlined /> },
          { key: 'move', label: '移动到…', icon: <SwapOutlined /> },
        )
      }
      if (rowCanDelete) {
        items.push(
          { type: 'divider' },
          { key: 'delete', label: '移入回收站', icon: <DeleteOutlined />, danger: true },
        )
      }
    }
    return items
  }

  function fallbackCopy(text: string) {
    const area = document.createElement('textarea')
    area.value = text
    area.setAttribute('readonly', '')
    area.style.position = 'fixed'
    area.style.top = '-1000px'
    document.body.appendChild(area)
    area.select()
    let ok = false
    try {
      ok = document.execCommand('copy')
    } catch {
      ok = false
    }
    document.body.removeChild(area)
    return ok
  }

  async function copyName(name: string) {
    let copied = false
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(name)
        copied = true
      } catch {
        copied = false
      }
    }
    if (!copied) copied = fallbackCopy(name)
    if (copied) {
      message.success('文件名已复制')
    } else {
      message.error('复制失败，请稍后重试')
    }
  }

  function handleRowAction(key: string, row: NodeItem) {
    switch (key) {
      case 'copy-name':
        void copyName(row.name)
        break
      case 'preview':
        openPreview(row)
        break
      case 'download':
        window.location.href = downloadUrl(row.id)
        break
      case 'history':
        setDrawerNode(row)
        setDrawerOpen(true)
        break
      case 'download-folder':
        window.location.href = folderDownloadUrl(row.id)
        break
      case 'rename':
        openRename(row)
        break
      case 'move':
        openMove(row)
        break
      case 'delete':
        confirmDelete(row)
        break
    }
  }

  function openRowMenu(event: ReactMouseEvent, row: NodeItem) {
    event.preventDefault()
    event.stopPropagation()
    setContextMenu({
      x: event.clientX,
      y: event.clientY,
      items: rowMenuItems(row),
      onSelect: (key) => handleRowAction(key, row),
    })
  }

  function areaMenuItems(): MenuProps['items'] {
    const items: MenuProps['items'] = []
    if (canWriteHere && !searching) {
      items.push(
        { key: 'new-node', label: folderId ? '新建文件夹' : '新建项目', icon: <FolderAddOutlined /> },
        { key: 'upload', label: '上传文件', icon: <UploadOutlined /> },
        { type: 'divider' },
      )
    }
    items.push({ key: 'refresh', label: '刷新', icon: <ReloadOutlined /> })
    return items
  }

  function handleAreaAction(key: string) {
    if (key === 'new-node') {
      setNewFolderOpen(true)
      return
    }
    if (key === 'upload') {
      uploaderRef.current?.pickFiles()
      return
    }
    if (key === 'refresh') {
      void load()
    }
  }

  function openAreaMenu(event: ReactMouseEvent) {
    event.preventDefault()
    setContextMenu({
      x: event.clientX,
      y: event.clientY,
      items: areaMenuItems(),
      onSelect: handleAreaAction,
    })
  }

  const moveSecureRoot = useMemo(
    () => moveFolders.find((folder) => folder.space === 'secure' && folder.is_zone_root) || null,
    [moveFolders],
  )

  const moveTreeData = useMemo(() => {
    if (!moveTarget) return []
    const space = moveTarget.space
    const blocked = new Set<string>([moveTarget.id])
    let changed = true
    while (changed) {
      changed = false
      moveFolders.forEach((folder) => {
        if (!blocked.has(folder.id) && blocked.has(folder.parent_id)) {
          blocked.add(folder.id)
          changed = true
        }
      })
    }
    const byParent = new Map<string, NodeItem[]>()
    moveFolders.forEach((folder) => {
      if (blocked.has(folder.id) || folder.space !== space || folder.is_zone_root) return
      const list = byParent.get(folder.parent_id) || []
      list.push(folder)
      byParent.set(folder.parent_id, list)
    })
    const build = (parent: string): MoveTreeNode[] =>
      (byParent.get(parent) || []).map((folder) => ({
        title: folder.name,
        fullPath: movePathLabel(folder.path, space),
        value: folder.id,
        children: build(folder.id),
      }))
    if (space === 'secure') {
      if (!moveSecureRoot) return []
      return [
        {
          title: '权限区（根目录）',
          fullPath: '权限区',
          value: moveSecureRoot.id,
          children: build(moveSecureRoot.id),
        },
      ]
    }
    return [{ title: '全部项目（根目录）', fullPath: '全部项目', value: '__root__', children: build('') }]
  }, [moveFolders, moveTarget, moveSecureRoot])

  const moveInitialExpandedKeys = useMemo(
    () =>
      moveExpandedKeys(
        moveFolders,
        moveTarget?.parent_id || '',
        moveTarget?.space === 'secure' && moveSecureRoot ? moveSecureRoot.id : '__root__',
      ),
    [moveFolders, moveTarget, moveSecureRoot],
  )

  const crumbItems: BreadcrumbProps['items'] = crumbs.map((crumb, index) => ({
    title:
      index === crumbs.length - 1 ? (
        <span>{crumb.name}</span>
      ) : (
        <a onClick={() => onFolderChange(crumb.id)}>{crumb.name}</a>
      ),
  }))

  const parentId = !searching && folderId && crumbs.length > 1 ? crumbs[crumbs.length - 2].id : null

  const columns: ColumnsType<NodeItem> = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
      ellipsis: true,
      render: (_, row) => (
        <span className="file-name">
          {row.kind === 'folder' ? (
            <FolderFilled style={{ color: '#5b9bd9', fontSize: 19 }} />
          ) : (
            fileIcon(row.name)
          )}
          <span className="file-name-text">{row.name}</span>
        </span>
      ),
    },
  ]

  if (searching) {
    columns.push({
      title: '所在位置',
      dataIndex: 'path',
      key: 'path',
      width: 260,
      ellipsis: true,
      render: (value: string) => <Text type="secondary">{value || '/'}</Text>,
    })
  }

  columns.push(
    {
      title: '类型',
      key: 'type',
      width: 110,
      render: (_, row) => {
        if (row.kind === 'folder') {
          return <Tag style={{ background: '#edf4fd', color: '#1f5c9e', borderColor: '#d8e6f8' }}>文件夹</Tag>
        }
        const dot = row.name.lastIndexOf('.')
        const ext = dot > 0 ? row.name.slice(dot + 1).toUpperCase() : ''
        return <span className="muted">{ext ? `${ext.length > 6 ? '文件' : ext}` : '文件'}</span>
      },
    },
    {
      title: '大小',
      dataIndex: 'size',
      key: 'size',
      width: 105,
      render: (value: number, row) => (row.kind === 'folder' ? <span className="muted">—</span> : formatBytes(value)),
    },
    {
      title: '版本',
      dataIndex: 'version_no',
      key: 'version',
      width: 80,
      render: (value: number, row) => (row.kind === 'file' ? `V${value}` : <span className="muted">—</span>),
    },
    { title: '更新人', dataIndex: 'updated_by', key: 'updated_by', width: 110, ellipsis: true },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 175,
      render: (value: string) => <span className="muted">{fmtTime(value)}</span>,
    },
    {
      title: '',
      key: 'actions',
      width: 128,
      fixed: 'right',
      render: (_, row) => {
        const items = rowMenuItems(row)
        if (!items?.length) return null
        return (
          <Space size={0}>
            <Tooltip title={row.kind === 'folder' ? '下载整个文件夹' : '下载当前版本'}>
              <Button
                type="text"
                icon={<DownloadOutlined />}
                onClick={(event) => {
                  event.stopPropagation()
                  window.location.href = row.kind === 'folder' ? folderDownloadUrl(row.id) : downloadUrl(row.id)
                }}
              />
            </Tooltip>
            <Dropdown
              trigger={['click']}
              menu={{
                items,
                onClick: ({ key, domEvent }) => {
                  domEvent.stopPropagation()
                  handleRowAction(key, row)
                },
              }}
            >
              <Button type="text" icon={<MoreOutlined />} />
            </Dropdown>
            {row.kind === 'file' && previewKindForName(row.name) && (
              <Tooltip title="预览">
                <Button
                  type="text"
                  icon={<EyeOutlined />}
                  onClick={(event) => {
                    event.stopPropagation()
                    openPreview(row)
                  }}
                />
              </Tooltip>
            )}
          </Space>
        )
      },
    },
  )

  return (
    <div
      className={`file-browser ${dragOver ? 'drag-over' : ''}`}
      onContextMenu={openAreaMenu}
      onDragEnter={(event) => {
        if (!canWriteHere || searching) return
        event.preventDefault()
        dragDepth.current += 1
        setDragOver(true)
      }}
      onDragOver={(event) => {
        if (!canWriteHere || searching) return
        event.preventDefault()
      }}
      onDragLeave={() => {
        dragDepth.current = Math.max(0, dragDepth.current - 1)
        if (dragDepth.current === 0) setDragOver(false)
      }}
      onDrop={(event) => {
        event.preventDefault()
        dragDepth.current = 0
        setDragOver(false)
        if (!canWriteHere || searching) return
        uploaderRef.current?.enqueueDrop(event.dataTransfer)
      }}
    >
      {dragOver && (
        <div className="drop-overlay">
          <InboxOutlined />
          <div>松手即可上传到此目录</div>
          <div className="drop-hint">支持文件或整个文件夹</div>
        </div>
      )}
      <div className="browser-toolbar">
        <div className="browser-location">
          {searching ? (
            <Space>
              <Tag icon={<SearchOutlined />} closable onClose={onClearSearch} color="#3a79bc">
                搜索：{searchQuery}
              </Tag>
              <Text type="secondary">共 {items.length} 个结果</Text>
            </Space>
          ) : (
            <Space size={8}>
              {parentId !== null && (
                <Tooltip title="返回上一级">
                  <Button
                    size="small"
                    icon={<ArrowLeftOutlined />}
                    disabled={loading}
                    onClick={() => onFolderChange(parentId)}
                  />
                </Tooltip>
              )}
              <Breadcrumb items={crumbItems} />
            </Space>
          )}
        </div>
        <Space wrap>
          {canWriteHere && !searching && (
            <>
              <Button icon={<FolderAddOutlined />} onClick={() => setNewFolderOpen(true)}>
                {folderId ? '新建文件夹' : '新建项目'}
              </Button>
              <Uploader
                ref={uploaderRef}
                folderId={folderId}
                maxFileSize={settings.max_file_size}
                secure={currentSpace === 'secure'}
                onUploaded={handleUploaded}
                onRefresh={() => void load()}
              />
            </>
          )}
          <Tooltip title="刷新">
            <Button icon={<ReloadOutlined />} onClick={() => void load()} />
          </Tooltip>
        </Space>
      </div>

      <Spin spinning={loading}>
        <Table
          className="file-table"
          rowKey="id"
          columns={columns}
          dataSource={items}
          pagination={false}
          size="middle"
          onRow={(row) => ({
            onDoubleClick: () => openNode(row),
            onContextMenu: (event) => openRowMenu(event, row),
          })}
          scroll={{ x: 960 }}
          locale={{
            emptyText: (
              <div className="empty-block">
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description={
                    searching
                      ? '没有找到匹配的文件或文件夹'
                      : canWriteHere
                        ? '这里还没有文件，拖入文件或点击“上传文件”开始'
                        : '这个文件夹还没有文件'
                  }
                />
              </div>
            ),
          }}
        />
      </Spin>

      <DetailsDrawer
        node={drawerNode}
        open={drawerOpen}
        user={user}
        onClose={() => setDrawerOpen(false)}
        onPreview={openPreview}
        onChanged={(updated) => {
          if (updated) setDrawerNode(updated)
          void load()
        }}
      />

      <PreviewModal node={previewNode} open={!!previewNode} onClose={() => setPreviewNode(null)} />

      <Modal
        title={folderId ? '新建文件夹' : '新建项目'}
        open={newFolderOpen}
        okText="创建"
        cancelText="取消"
        confirmLoading={saving}
        onCancel={() => setNewFolderOpen(false)}
        onOk={() => void createFolder()}
      >
        <Input
          autoFocus
          value={newFolderName}
          maxLength={200}
          placeholder={folderId ? '例如：2026 年合同' : '例如：2026 年新项目'}
          onChange={(event) => setNewFolderName(event.target.value)}
          onPressEnter={() => void createFolder()}
        />
      </Modal>

      <Modal
        title="重命名"
        open={!!renameTarget}
        okText="保存"
        cancelText="取消"
        confirmLoading={saving}
        onCancel={() => setRenameTarget(null)}
        onOk={() => void renameNode()}
      >
        <Input
          autoFocus
          value={renameName}
          maxLength={Math.max(1, 200 - renameExt.length)}
          addonAfter={renameExt || undefined}
          onChange={(event) => setRenameName(event.target.value)}
          onPressEnter={() => void renameNode()}
        />
      </Modal>

      <Modal
        title={`移动「${moveTarget?.name || ''}」`}
        open={!!moveTarget}
        okText="移动"
        cancelText="取消"
        confirmLoading={saving}
        onCancel={() => setMoveTarget(null)}
        onOk={() => void moveNode()}
      >
        <div style={{ marginBottom: 10 }}>
          <Text type="secondary">选择目标文件夹：</Text>
        </div>
        {moveTarget ? (
          moveFoldersReady ? (
            <TreeSelect
              style={{ width: '100%' }}
              value={moveParent}
              treeData={moveTreeData}
              treeDefaultExpandedKeys={moveInitialExpandedKeys}
              treeNodeLabelProp="fullPath"
              showSearch
              treeNodeFilterProp="title"
              onChange={(value) => setMoveParent(value)}
              placeholder="选择目标文件夹"
            />
          ) : (
            <div style={{ padding: '8px 0' }}>
              <Spin size="small" />
            </div>
          )
        ) : null}
      </Modal>

      <ContextMenu state={contextMenu} onClose={() => setContextMenu(null)} />
    </div>
  )
})
