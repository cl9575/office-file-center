import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from 'react'
import {
  Badge,
  Button,
  Drawer,
  Input,
  List,
  Modal,
  Progress,
  Space,
  Tag,
  Tooltip,
  Typography,
  App,
} from 'antd'
import {
  CheckCircleFilled,
  CloseCircleFilled,
  CloudUploadOutlined,
  FolderOpenOutlined,
  InboxOutlined,
  UploadOutlined,
} from '@ant-design/icons'
import dayjs from 'dayjs'
import { api, ApiError, formatBytes, newUploadId } from '../api'
import type { NodeItem, UploadResult, VisibilityChoice } from '../types'
import { VisibilityModal } from './VisibilityModal'

const { Text } = Typography

type UploadStatus = 'waiting' | 'uploading' | 'done' | 'failed' | 'canceled'

interface UploadTarget {
  name: string
  parentId: string
  nodeId?: string
  baseVersionId?: string
  label?: string
  visibility?: VisibilityChoice
}

interface IncomingEntry {
  file: File
  relPath: string
}

interface QueueItem {
  id: string
  file: File
  uploadId: string
  status: UploadStatus
  progress: number
  error?: string
  batch: string
  target: UploadTarget
}

interface UploadBatch {
  name: string
  total: number
  pending: number
  ok: number
  failed: number
  canceled: number
  summarized: boolean
}

interface ConflictChoice {
  mode: 'replace' | 'rename'
  name?: string
}

interface ConflictState {
  file: File
  existing?: NodeItem
}

function readFileEntry(entry: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject))
}

function readAllEntries(dir: FileSystemDirectoryEntry): Promise<FileSystemEntry[]> {
  const reader = dir.createReader()
  return new Promise((resolve, reject) => {
    const all: FileSystemEntry[] = []
    const next = () => {
      reader.readEntries(
        (batch) => {
          if (!batch.length) {
            resolve(all)
            return
          }
          all.push(...batch)
          next()
        },
        reject,
      )
    }
    next()
  })
}

async function walkEntry(entry: FileSystemEntry, prefix: string, out: IncomingEntry[]) {
  const relPath = prefix ? `${prefix}/${entry.name}` : entry.name
  if (entry.isFile) {
    out.push({ file: await readFileEntry(entry as FileSystemFileEntry), relPath })
    return
  }
  if (entry.isDirectory) {
    for (const child of await readAllEntries(entry as FileSystemDirectoryEntry)) {
      await walkEntry(child, relPath, out)
    }
  }
}

function invalidName(name: string): boolean {
  if (!name || name === '.' || name === '..' || name.endsWith('.')) return true
  if (Array.from(name).length > 200) return true
  return /[\\/:*?"<>|\u0000-\u001f]/.test(name)
}

export interface UploaderHandle {
  enqueueFiles: (files: File[]) => void
  enqueueDrop: (transfer: DataTransfer) => void
  pickFiles: () => void
  pickFolder: () => void
}

export const Uploader = forwardRef<
  UploaderHandle,
  {
    folderId: string
    maxFileSize: number
    secure?: boolean
    disabled?: boolean
    onUploaded: (result: UploadResult) => void
    onRefresh: () => void
  }
>(function Uploader({ folderId, maxFileSize, secure, disabled, onUploaded, onRefresh }, ref) {
  const { message } = App.useApp()
  const [queue, setQueue] = useState<QueueItem[]>([])
  const queueRef = useRef<QueueItem[]>([])
  const [open, setOpen] = useState(false)
  const activeRef = useRef(0)
  const abortRefs = useRef<Map<string, () => void>>(new Map())
  const batches = useRef<Map<string, UploadBatch>>(new Map())
  const inputRef = useRef<HTMLInputElement>(null)
  const folderInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    folderInputRef.current?.setAttribute('webkitdirectory', '')
    folderInputRef.current?.setAttribute('directory', '')
  }, [])

  const [conflict, setConflict] = useState<ConflictState | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const conflictResolve = useRef<((choice: ConflictChoice | null) => void) | null>(null)
  const [visibilityOpen, setVisibilityOpen] = useState(false)
  const visibilityResolve = useRef<((choice: VisibilityChoice | null) => void) | null>(null)

  function askVisibility(): Promise<VisibilityChoice | null | undefined> {
    if (!secure) return Promise.resolve(undefined)
    return new Promise((resolve) => {
      visibilityResolve.current = resolve
      setVisibilityOpen(true)
    })
  }

  function finishVisibility(choice: VisibilityChoice | null) {
    setVisibilityOpen(false)
    const resolve = visibilityResolve.current
    visibilityResolve.current = null
    resolve?.(choice)
  }

  function setQ(updater: (prev: QueueItem[]) => QueueItem[]) {
    queueRef.current = updater(queueRef.current)
    setQueue([...queueRef.current])
  }

  function registerBatch(id: string, name: string, total: number) {
    batches.current.set(id, { name, total, pending: total, ok: 0, failed: 0, canceled: 0, summarized: false })
  }

  function settleItem(item: QueueItem, outcome: 'ok' | 'failed' | 'canceled') {
    const batch = batches.current.get(item.batch)
    if (!batch) return
    batch.pending = Math.max(0, batch.pending - 1)
    if (outcome === 'ok') batch.ok += 1
    else if (outcome === 'failed') batch.failed += 1
    else batch.canceled += 1
    if (batch.pending > 0 || batch.summarized) return
    batch.summarized = true
    if (batch.ok === 0 && batch.failed === 0) return
    if (batch.failed === 0 && batch.canceled === 0) {
      if (batch.total === 1) message.success(`「${batch.name}」上传成功`)
      else message.success(`「${batch.name}」上传完成，共 ${batch.ok} 个文件`)
      return
    }
    const parts = [`成功 ${batch.ok} 个`]
    if (batch.failed > 0) parts.push(`失败 ${batch.failed} 个`)
    if (batch.canceled > 0) parts.push(`取消 ${batch.canceled} 个`)
    message.warning(
      `「${batch.name}」上传结束：${parts.join('，')}${batch.failed > 0 ? '（失败原因见上传队列）' : ''}`,
    )
  }

  function reopenItem(item: QueueItem) {
    const batch = batches.current.get(item.batch)
    if (!batch) return
    batch.pending += 1
    if (batch.failed > 0) batch.failed -= 1
    batch.summarized = false
  }

  function pump() {
    while (activeRef.current < 2) {
      const next = queueRef.current.find((item) => item.status === 'waiting')
      if (!next) return
      next.status = 'uploading'
      setQ((prev) => prev.map((item) => (item.id === next.id ? { ...item, status: 'uploading' } : item)))
      activeRef.current += 1
      startUpload(next)
    }
  }

  function startUpload(item: QueueItem) {
    const handle = api.upload({
      file: item.file,
      uploadId: item.uploadId,
      parentId: item.target.parentId,
      name: item.target.name,
      nodeId: item.target.nodeId,
      baseVersionId: item.target.baseVersionId,
      visibility: item.target.visibility,
      onProgress: (percent) => {
        setQ((prev) => prev.map((row) => (row.id === item.id ? { ...row, progress: percent } : row)))
      },
    })
    abortRefs.current.set(item.id, handle.abort)
    handle.promise
      .then((result) => {
        setQ((prev) =>
          prev.map((row) =>
            row.id === item.id ? { ...row, status: 'done', progress: 100, error: undefined } : row,
          ),
        )
        settleItem(item, 'ok')
        onUploaded(result)
      })
      .catch((err) => {
        const error = err as ApiError
        if (error.code === 'ABORTED') {
          setQ((prev) => prev.map((row) => (row.id === item.id ? { ...row, status: 'canceled' } : row)))
          settleItem(item, 'canceled')
        } else {
          setQ((prev) =>
            prev.map((row) =>
              row.id === item.id ? { ...row, status: 'failed', error: error.message } : row,
            ),
          )
          settleItem(item, 'failed')
        }
      })
      .finally(() => {
        abortRefs.current.delete(item.id)
        activeRef.current = Math.max(0, activeRef.current - 1)
        pump()
      })
  }

  function suggestName(name: string): string {
    const dot = name.lastIndexOf('.')
    if (dot <= 0) return `${name} (1)`
    return `${name.slice(0, dot)} (1)${name.slice(dot)}`
  }

  function askConflict(file: File, existing?: NodeItem): Promise<ConflictChoice | null> {
    return new Promise((resolve) => {
      conflictResolve.current = resolve
      setRenameValue(suggestName(file.name))
      setConflict({ file, existing })
    })
  }

  function finishConflict(choice: ConflictChoice | null) {
    setConflict(null)
    const resolve = conflictResolve.current
    conflictResolve.current = null
    resolve?.(choice)
  }

  async function prepareTarget(file: File): Promise<UploadTarget | null> {
    const resolved = await api.resolve(folderId, file.name)
    if (!resolved.exists) {
      return { name: file.name, parentId: folderId }
    }
    if (!resolved.node) {
      const choice = await askConflict(file)
      if (!choice) return null
      return { name: (choice.name || '').trim(), parentId: folderId }
    }
    const existing = resolved.node
    const choice = await askConflict(file, existing)
    if (!choice) return null
    if (choice.mode === 'replace') {
      if (existing.kind !== 'file') {
        message.error('同名项目是文件夹，请改名后再上传')
        return null
      }
      return {
        name: existing.name,
        parentId: folderId,
        nodeId: existing.id,
        baseVersionId: existing.current_version_id,
      }
    }
    return { name: (choice.name || '').trim(), parentId: folderId }
  }

  async function addFiles(files: File[], presetVisibility?: VisibilityChoice) {
    if (disabled) return
    if (!files.length) return
    let visibility = presetVisibility
    if (secure && !visibility) {
      const chosen = await askVisibility()
      if (!chosen) return
      visibility = chosen
    }
    const batchId = newUploadId()
    const batchName = files.length === 1 ? files[0].name : '本次上传'
    const created: QueueItem[] = []
    for (const file of files) {
      if (file.size > maxFileSize) {
        message.error(`「${file.name}」超过单个文件 ${formatBytes(maxFileSize)} 的限制`)
        continue
      }
      try {
        const target = await prepareTarget(file)
        if (!target) continue
        if (!target.name) {
          message.error('文件名不能为空')
          continue
        }
        created.push({
          id: newUploadId(),
          file,
          uploadId: newUploadId(),
          status: 'waiting',
          progress: 0,
          batch: batchId,
          target: { ...target, label: file.name, visibility: target.nodeId ? undefined : visibility },
        })
      } catch (err) {
        message.error(err instanceof ApiError ? err.message : `无法检查「${file.name}」`)
      }
    }
    if (!created.length) return
    registerBatch(batchId, batchName, created.length)
    setQ((prev) => [...prev, ...created])
    setOpen(true)
    pump()
  }

  async function addEntries(entries: IncomingEntry[]) {
    if (disabled || !entries.length) return
    let visibility: VisibilityChoice | undefined
    if (secure) {
      const chosen = await askVisibility()
      if (!chosen) return
      visibility = chosen
    }
    const plain: File[] = []
    const groups = new Map<string, IncomingEntry[]>()
    for (const entry of entries) {
      const rel = entry.relPath.replace(/\\/g, '/')
      if (!rel.includes('/')) {
        plain.push(entry.file)
        continue
      }
      const root = rel.split('/')[0]
      const list = groups.get(root)
      if (list) list.push({ file: entry.file, relPath: rel })
      else groups.set(root, [{ file: entry.file, relPath: rel }])
    }
    if (plain.length) await addFiles(plain, visibility)
    for (const [root, list] of groups) {
      await addFolderGroup(root, list, visibility)
    }
  }

  async function addFolderGroup(root: string, entries: IncomingEntry[], visibility?: VisibilityChoice) {
    if (invalidName(root)) {
      message.error(`文件夹「${root}」的名称不符合要求，已跳过`)
      return
    }
    const usable = entries.filter((entry) => {
      const parts = entry.relPath.split('/')
      return entry.file.size <= maxFileSize && !invalidName(parts[parts.length - 1])
    })
    if (!usable.length) {
      message.warning(`「${root}」没有可上传的文件（跳过 ${entries.length} 个）`)
      return
    }
    let rootId = ''
    try {
      const resolved = await api.resolve(folderId, root)
      if (resolved.exists && resolved.node) {
        if (resolved.node.kind !== 'folder') {
          message.error(`当前位置已存在同名的文件「${root}」，请改名后再上传文件夹`)
          return
        }
        rootId = resolved.node.id
      } else {
        const created = await api.createFolder(folderId, root)
        rootId = created.id
      }
    } catch (err) {
      message.error(`准备文件夹「${root}」失败：${err instanceof Error ? err.message : '未知错误'}`)
      return
    }

    const hide = message.loading(`正在准备「${root}」的目录结构…`, 0)
    try {
      const dirPaths = new Set<string>()
      for (const entry of usable) {
        const parts = entry.relPath.split('/')
        for (let i = 1; i < parts.length; i++) {
          dirPaths.add(parts.slice(0, i).join('/'))
        }
      }
      const dirs = Array.from(dirPaths).sort((a, b) => {
        const depth = a.split('/').length - b.split('/').length
        return depth !== 0 ? depth : a.localeCompare(b)
      })
      const dirIds = new Map<string, string>([[root, rootId]])
      for (const dir of dirs) {
        if (dirIds.has(dir)) continue
        const parts = dir.split('/')
        const parentId = dirIds.get(parts.slice(0, -1).join('/'))
        const name = parts[parts.length - 1]
        if (!parentId || invalidName(name)) {
          dirIds.set(dir, '')
          continue
        }
        try {
          const resolved = await api.resolve(parentId, name)
          if (resolved.exists && resolved.node) {
            dirIds.set(dir, resolved.node.kind === 'folder' ? resolved.node.id : '')
          } else {
            const created = await api.createFolder(parentId, name)
            dirIds.set(dir, created.id)
          }
        } catch {
          try {
            const again = await api.resolve(parentId, name)
            dirIds.set(dir, again.exists && again.node?.kind === 'folder' ? again.node.id : '')
          } catch {
            dirIds.set(dir, '')
          }
        }
      }

      const children = new Map<string, Map<string, NodeItem>>()
      async function childrenOf(id: string) {
        const cached = children.get(id)
        if (cached) return cached
        const items = await api.listNodes(id)
        const map = new Map(items.map((item) => [item.name, item]))
        children.set(id, map)
        return map
      }

      const created: QueueItem[] = []
      const batchId = newUploadId()
      const taken = new Set<string>()
      let replaced = 0
      let skipped = entries.length - usable.length
      for (const entry of usable) {
        const parts = entry.relPath.split('/')
        const name = parts[parts.length - 1]
        const parentId = dirIds.get(parts.slice(0, -1).join('/'))
        if (!parentId) {
          skipped += 1
          continue
        }
        const key = `${parentId}/${name}`
        if (taken.has(key)) {
          skipped += 1
          continue
        }
        taken.add(key)
        try {
          const existing = (await childrenOf(parentId)).get(name)
          if (existing && existing.kind === 'folder') {
            skipped += 1
            continue
          }
          created.push({
            id: newUploadId(),
            file: entry.file,
            uploadId: newUploadId(),
            status: 'waiting',
            progress: 0,
            batch: batchId,
            target: {
              name: existing?.name || name,
              parentId,
              nodeId: existing?.id,
              baseVersionId: existing?.current_version_id,
              label: entry.relPath,
              visibility: existing ? undefined : visibility,
            },
          })
          if (existing) replaced += 1
        } catch (err) {
          skipped += 1
          message.error(`检查「${entry.relPath}」失败：${err instanceof Error ? err.message : '未知错误'}`)
        }
      }

      if (!created.length) {
        message.warning(`「${root}」没有可上传的文件${skipped ? `（跳过 ${skipped} 个）` : ''}`)
        return
      }
      if (replaced || skipped) {
        message.info(
          `「${root}」已加入 ${created.length} 个文件${replaced ? `，其中 ${replaced} 个覆盖同名文件` : ''}${
            skipped ? `，跳过 ${skipped} 个` : ''
          }`,
        )
      }
      registerBatch(batchId, root, created.length)
      setQ((prev) => [...prev, ...created])
      setOpen(true)
      pump()
    } finally {
      hide()
    }
  }

  async function enqueueDrop(transfer: DataTransfer) {
    if (disabled) return
    const entries: FileSystemEntry[] = []
    for (const item of Array.from(transfer.items || [])) {
      if (item.kind !== 'file') continue
      const entry = item.webkitGetAsEntry?.()
      if (entry) entries.push(entry)
    }
    if (!entries.length) {
      const files = Array.from(transfer.files || [])
      if (files.length) await addFiles(files)
      return
    }
    const incoming: IncomingEntry[] = []
    for (const entry of entries) {
      await walkEntry(entry, '', incoming)
    }
    await addEntries(incoming)
  }

  useImperativeHandle(ref, () => ({
    enqueueFiles: (files: File[]) => {
      void addFiles(files)
    },
    enqueueDrop: (transfer: DataTransfer) => {
      void enqueueDrop(transfer)
    },
    pickFiles: () => inputRef.current?.click(),
    pickFolder: () => folderInputRef.current?.click(),
  }))

  const activeCount = queue.filter((item) => item.status === 'uploading' || item.status === 'waiting').length

  return (
    <>
      <input
        ref={inputRef}
        type="file"
        multiple
        style={{ display: 'none' }}
        onChange={(event) => {
          const files = Array.from(event.target.files || [])
          event.target.value = ''
          void addFiles(files)
        }}
      />
      <input
        ref={folderInputRef}
        type="file"
        multiple
        style={{ display: 'none' }}
        onChange={(event) => {
          const files = Array.from(event.target.files || [])
          event.target.value = ''
          void addEntries(
            files.map((file) => ({
              file,
              relPath: (file.webkitRelativePath || file.name).replace(/^\.?\//, ''),
            })),
          )
        }}
      />
      <Space size={10}>
        <Badge count={activeCount} size="small" offset={[-2, 2]}>
          <Button
            type="primary"
            icon={<UploadOutlined />}
            disabled={disabled}
            onClick={() => inputRef.current?.click()}
          >
            上传文件
          </Button>
        </Badge>
        <Tooltip title="按原目录结构上传；同名文件会覆盖为新版本，空文件夹不会上传">
          <Button icon={<FolderOpenOutlined />} disabled={disabled} onClick={() => folderInputRef.current?.click()}>
            上传文件夹
          </Button>
        </Tooltip>
        {queue.length > 0 && (
          <Button icon={<CloudUploadOutlined />} onClick={() => setOpen(true)}>
            队列 {queue.filter((i) => i.status === 'done').length}/{queue.length}
          </Button>
        )}
      </Space>
      <Drawer
        title="上传队列"
        placement="bottom"
        height={340}
        open={open}
        onClose={() => setOpen(false)}
        extra={
          <Button
            size="small"
            onClick={() => {
              setQ((prev) => prev.filter((item) => item.status === 'uploading' || item.status === 'waiting'))
            }}
          >
            清除已结束
          </Button>
        }
      >
        <List
          dataSource={[...queue].reverse()}
          locale={{ emptyText: '把文件或文件夹拖进页面，或点击“上传文件/上传文件夹”开始' }}
          renderItem={(item) => (
            <List.Item
              actions={[
                item.status === 'uploading' || item.status === 'waiting' ? (
                  <Button
                    key="cancel"
                    type="link"
                    size="small"
                    onClick={() => {
                      if (item.status === 'uploading') {
                        abortRefs.current.get(item.id)?.()
                      } else {
                        setQ((prev) =>
                          prev.map((row) => (row.id === item.id ? { ...row, status: 'canceled' } : row)),
                        )
                        settleItem(item, 'canceled')
                      }
                    }}
                  >
                    取消
                  </Button>
                ) : item.status === 'failed' ? (
                  <Button
                    key="retry"
                    type="link"
                    size="small"
                    onClick={async () => {
                      if (item.error?.includes('已被他人更新') || item.error?.includes('同名')) {
                        message.info('请重新选择文件上传，以确认最新的文件状态')
                        return
                      }
                      try {
                        const status = await api.uploadStatus(item.uploadId)
                        if (status.status === 'completed') {
                          reopenItem(item)
                          setQ((prev) =>
                            prev.map((row) =>
                              row.id === item.id ? { ...row, status: 'done', progress: 100, error: undefined } : row,
                            ),
                          )
                          settleItem(item, 'ok')
                          onRefresh()
                          return
                        }
                        if (status.status === 'pending') {
                          message.info('服务器仍在处理该上传，请稍后再次重试')
                          return
                        }
                        if (status.error?.includes('已被他人更新') || status.error?.includes('同名')) {
                          message.info('文件状态已变化，请重新选择文件上传')
                          return
                        }
                      } catch (err) {
                        if (!(err instanceof ApiError) || err.status !== 404) {
                          message.error(err instanceof Error ? err.message : '无法确认上传状态')
                          return
                        }
                      }
                      reopenItem(item)
                      setQ((prev) =>
                        prev.map((row) =>
                          row.id === item.id
                            ? { ...row, status: 'waiting', progress: 0, error: undefined }
                            : row,
                        ),
                      )
                      pump()
                    }}
                  >
                    重试
                  </Button>
                ) : null,
              ]}
            >
              <List.Item.Meta
                avatar={
                  item.status === 'done' ? (
                    <CheckCircleFilled style={{ fontSize: 24, color: '#587d63' }} />
                  ) : item.status === 'failed' || item.status === 'canceled' ? (
                    <CloseCircleFilled style={{ fontSize: 24, color: '#a9635d' }} />
                  ) : (
                    <CloudUploadOutlined style={{ fontSize: 24, color: '#3a79bc' }} />
                  )
                }
                title={
                  <Space size={8} wrap>
                    <Text>{item.target.label || item.target.name}</Text>
                    {item.target.nodeId && <Tag color="#3a79bc">新版本</Tag>}
                    <Text type="secondary" style={{ fontSize: 12 }}>{formatBytes(item.file.size)}</Text>
                  </Space>
                }
                description={
                  <>
                    {item.status === 'uploading' && (
                      <Progress
                        percent={item.progress}
                        size="small"
                        status={item.progress >= 100 ? 'active' : 'normal'}
                        format={(p) => (p && p >= 100 ? '服务器处理中…' : `${p || 0}%`)}
                      />
                    )}
                    {item.status === 'waiting' && <Text type="secondary">等待上传</Text>}
                    {item.status === 'done' && <Text type="success">已完成</Text>}
                    {item.status === 'canceled' && <Text type="secondary">已取消</Text>}
                    {item.status === 'failed' && <Text type="danger">{item.error}</Text>}
                  </>
                }
              />
            </List.Item>
          )}
        />
      </Drawer>

      <Modal
        open={!!conflict}
        title={conflict?.existing ? '发现同名文件' : '名称已存在'}
        closable
        maskClosable={false}
        onCancel={() => finishConflict(null)}
        footer={[
          <Button key="skip" type="text" onClick={() => finishConflict(null)}>
            跳过该文件
          </Button>,
          <Button
            key="rename"
            onClick={() => {
              const name = renameValue.trim()
              if (!name) {
                message.error('请输入新文件名')
                return
              }
              if (name === conflict?.existing?.name) {
                message.error('新文件名不能与原名相同')
                return
              }
              finishConflict({ mode: 'rename', name })
            }}
          >
            使用新名称上传
          </Button>,
          <Tooltip
            key="replace"
            title={
              !conflict?.existing
                ? '同名项目不可见，无法替换'
                : conflict.existing.kind === 'folder'
                  ? '目标是文件夹，不能替换'
                  : undefined
            }
          >
            <Button
              type="primary"
              disabled={conflict?.existing?.kind !== 'file'}
              onClick={() => finishConflict({ mode: 'replace' })}
            >
              替换为新版本
            </Button>
          </Tooltip>,
        ]}
      >
        {conflict && (
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            {conflict.existing ? (
              <div className="conflict-summary">
                <InboxOutlined style={{ fontSize: 32, color: '#3a79bc' }} />
                <div>
                  <div><Text strong>{conflict.existing.name}</Text></div>
                  <Text type="secondary">
                    当前为 {conflict.existing.kind === 'folder' ? '文件夹' : `第 ${conflict.existing.version_no} 版`}，
                    最后由 {conflict.existing.updated_by} 于 {dayjs(conflict.existing.updated_at).format('YYYY-MM-DD HH:mm')} 更新
                  </Text>
                </div>
              </div>
            ) : (
              <Text>同名项目已存在，请使用其他名称。</Text>
            )}
            {conflict.existing && (
              <Text type="secondary">
                选择“替换为新版本”会保留现有历史并生成新版本；也可以修改下面的名称，作为另一个文件上传。
              </Text>
            )}
            <Input
              value={renameValue}
              onChange={(event) => setRenameValue(event.target.value)}
              addonBefore="新名称"
              maxLength={200}
            />
          </Space>
        )}
      </Modal>

      <VisibilityModal
        open={visibilityOpen}
        onCancel={() => finishVisibility(null)}
        onOk={(choice) => finishVisibility(choice)}
      />
    </>
  )
})
