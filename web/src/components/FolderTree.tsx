import { useEffect, useMemo, useRef, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import { Spin, Tooltip, Tree } from 'antd'
import type { MenuProps } from 'antd'
import {
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  FolderAddOutlined,
  FolderFilled,
  HomeFilled,
  LockFilled,
  PlusOutlined,
  ProjectFilled,
  ReloadOutlined,
  SwapOutlined,
  UploadOutlined,
} from '@ant-design/icons'
import type { DataNode } from 'antd/es/tree'
import { api } from '../api'
import type { NodeItem } from '../types'
import { ContextMenu, type ContextMenuState } from './ContextMenu'

const MAIN_ROOT_KEY = '__main_root__'

let rememberedExpansion: string[] = [MAIN_ROOT_KEY]
let hasRememberedExpansion = false

function visibleExpandedKeys(folders: NodeItem[], keys: string[]): string[] {
  const byId = new Map(folders.map((folder) => [folder.id, folder]))
  const expanded = new Set(keys)
  const visible = keys.filter((key) => {
    if (key === MAIN_ROOT_KEY) return true
    let node = byId.get(key)
    if (!node) return false
    let parent = node.parent_id
    while (parent) {
      if (!expanded.has(parent)) return false
      node = byId.get(parent)
      if (!node) return false
      parent = node.parent_id
    }
    return node.space !== 'main' || expanded.has(MAIN_ROOT_KEY)
  })
  if (!visible.includes(MAIN_ROOT_KEY)) visible.unshift(MAIN_ROOT_KEY)
  return visible
}

export function FolderTree({
  selected,
  version,
  canEdit,
  canUploadSecure,
  isOwner,
  onSelect,
  onNewProject,
  onNodeAction,
  onAreaAction,
}: {
  selected: string
  version: number
  canEdit: boolean
  canUploadSecure: boolean
  isOwner: boolean
  onSelect: (id: string) => void
  onNewProject: () => void
  onNodeAction: (action: string, node: NodeItem) => void
  onAreaAction: (action: string) => void
}) {
  const [folders, setFolders] = useState<NodeItem[]>([])
  const [loading, setLoading] = useState(true)
  const [expandedKeys, setExpandedKeys] = useState<string[]>(rememberedExpansion)
  const followedSelection = useRef<string | null>(hasRememberedExpansion ? selected : null)
  const [menu, setMenu] = useState<ContextMenuState | null>(null)

  useEffect(() => {
    let alive = true
    setLoading(true)
    api
      .folderTree()
      .then((items) => {
        if (alive) setFolders(items)
      })
      .catch(() => {
        if (alive) setFolders([])
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [version])

  const secureRoot = useMemo(() => folders.find((folder) => folder.is_zone_root) || null, [folders])
  const selectedNode = useMemo(
    () => folders.find((folder) => folder.id === selected) || null,
    [folders, selected],
  )

  useEffect(() => {
    if (!selected) {
      followedSelection.current = null
      return
    }
    if (followedSelection.current === selected) return
    const byId = new Map(folders.map((folder) => [folder.id, folder]))
    const active = byId.get(selected)
    if (!active) return
    const ancestors: string[] = []
    let node: NodeItem | undefined = active
    while (node && node.parent_id) {
      ancestors.push(node.parent_id)
      node = byId.get(node.parent_id)
    }
    if (node?.space === 'main') ancestors.push(MAIN_ROOT_KEY)
    followedSelection.current = selected
    if (!ancestors.length) return
    setExpandedKeys((current) => {
      const next = new Set(current)
      const before = next.size
      ancestors.forEach((id) => next.add(id))
      if (next.size === before) return current
      const result = visibleExpandedKeys(folders, Array.from(next))
      rememberedExpansion = result
      hasRememberedExpansion = true
      return result
    })
  }, [selected, folders])

  const treeData = useMemo<DataNode[]>(() => {
    const byParent = new Map<string, NodeItem[]>()
    folders.forEach((folder) => {
      if (folder.is_zone_root) return
      const list = byParent.get(folder.parent_id) || []
      list.push(folder)
      byParent.set(folder.parent_id, list)
    })
    const build = (parent: string, space: string, depth: number): DataNode[] =>
      (byParent.get(parent) || [])
        .filter((folder) => folder.space === space)
        .map((folder) => {
          const isProject = depth === 0 && space === 'main'
          return {
            key: folder.id,
            title: (
              <span className={isProject ? 'folder-tree-project' : undefined} title={folder.path}>
                {folder.name}
              </span>
            ),
            icon: isProject ? (
              <ProjectFilled style={{ color: '#3a79bc' }} />
            ) : (
              <FolderFilled style={{ color: '#5b9bd9' }} />
            ),
            children: build(folder.id, space, depth + 1),
          }
        })

    const mainRoot: DataNode = {
      key: MAIN_ROOT_KEY,
      className: 'folder-tree-root-node',
      icon: <HomeFilled style={{ color: '#3a79bc' }} />,
      title: (
        <span className="folder-tree-root-title">
          <span>全部项目</span>
          {canEdit && (
            <Tooltip title="新建项目">
              <button
                type="button"
                className="folder-tree-root-add"
                onClick={(event) => {
                  event.stopPropagation()
                  onNewProject()
                }}
              >
                <PlusOutlined />
              </button>
            </Tooltip>
          )}
        </span>
      ),
      children: build('', 'main', 0),
    }
    const data = [mainRoot]
    if (secureRoot) {
      data.push({
        key: secureRoot.id,
        className: 'folder-tree-root-node folder-tree-secure-root',
        icon: <LockFilled style={{ color: '#7d6f5a' }} />,
        title: (
          <span className="folder-tree-root-title">
            <span>{secureRoot.name}</span>
            {canUploadSecure && (
              <Tooltip title="新建文件夹">
                <button
                  type="button"
                  className="folder-tree-root-add"
                  onClick={(event) => {
                    event.stopPropagation()
                    onNodeAction('new-folder', secureRoot)
                  }}
                >
                  <PlusOutlined />
                </button>
              </Tooltip>
            )}
          </span>
        ),
        children: build(secureRoot.id, 'secure', 0),
      })
    }
    return data
  }, [folders, secureRoot, canEdit, canUploadSecure, onNewProject, onNodeAction])

  function canManageFolder(node: NodeItem) {
    if (node.is_zone_root) return false
    if (node.space === 'secure') return isOwner
    return canEdit
  }

  function nodeMenuItems(node: NodeItem): MenuProps['items'] {
    const items: MenuProps['items'] = []
    if (node.is_zone_root) {
      if (canUploadSecure) {
        items.push({ key: 'new-folder', label: '新建文件夹', icon: <FolderAddOutlined /> })
      }
      items.push({ key: 'download', label: '下载文件夹（ZIP）', icon: <DownloadOutlined /> })
      return items
    }
    if (node.space === 'secure') {
      if (canUploadSecure) {
        items.push({ key: 'new-folder', label: '新建文件夹', icon: <FolderAddOutlined /> })
      }
    } else if (canEdit) {
      items.push({ key: 'new-folder', label: '新建文件夹', icon: <FolderAddOutlined /> })
    }
    items.push({ key: 'download', label: '下载文件夹（ZIP）', icon: <DownloadOutlined /> })
    if (canManageFolder(node)) {
      items.push(
        { type: 'divider' },
        { key: 'rename', label: '重命名', icon: <EditOutlined /> },
        { key: 'move', label: '移动到…', icon: <SwapOutlined /> },
        { type: 'divider' },
        { key: 'delete', label: '移入回收站', icon: <DeleteOutlined />, danger: true },
      )
    }
    return items
  }

  function areaMenuItems(): MenuProps['items'] {
    const items: MenuProps['items'] = []
    const secure = selected === secureRoot?.id || selectedNode?.space === 'secure'
    if (secure) {
      if (canUploadSecure) {
        items.push({ key: 'new-folder', label: '新建文件夹', icon: <FolderAddOutlined /> })
        items.push({ key: 'upload', label: '上传文件', icon: <UploadOutlined /> })
        items.push({ type: 'divider' })
      }
    } else if (canEdit) {
      items.push({ key: 'new-project', label: '新建项目', icon: <FolderAddOutlined /> })
      if (!selected) {
        items.push({ key: 'upload', label: '上传文件', icon: <UploadOutlined /> })
      }
      items.push({ type: 'divider' })
    }
    items.push({ key: 'refresh', label: '刷新', icon: <ReloadOutlined /> })
    return items
  }

  function toggleExpanded(key: string) {
    const raw = expandedKeys.includes(key)
      ? expandedKeys.filter((item) => item !== key)
      : [...expandedKeys, key]
    const next = visibleExpandedKeys(folders, raw)
    rememberedExpansion = next
    hasRememberedExpansion = true
    setExpandedKeys(next)
  }

  function openNodeMenu(event: ReactMouseEvent, node: NodeItem) {
    event.preventDefault()
    event.stopPropagation()
    setMenu({
      x: event.clientX,
      y: event.clientY,
      items: nodeMenuItems(node),
      onSelect: (key) => onNodeAction(key, node),
    })
  }

  function rootMenuItems(): MenuProps['items'] {
    const items: MenuProps['items'] = []
    if (canEdit) {
      items.push({ key: 'new-project', label: '新建项目', icon: <FolderAddOutlined /> })
      if (!selected) {
        items.push({ key: 'upload', label: '上传文件', icon: <UploadOutlined /> })
      }
      items.push({ type: 'divider' })
    }
    items.push({ key: 'refresh', label: '刷新', icon: <ReloadOutlined /> })
    return items
  }

  function openAreaMenu(event: ReactMouseEvent) {
    event.preventDefault()
    event.stopPropagation()
    setMenu({
      x: event.clientX,
      y: event.clientY,
      items: areaMenuItems(),
      onSelect: onAreaAction,
    })
  }

  return (
    <div
      className="folder-tree"
      onContextMenu={(event) => {
        const target = event.target as HTMLElement
        if (target.closest('.ant-tree-treenode')) return
        openAreaMenu(event)
      }}
    >
      <div className="folder-tree-title">文件目录</div>
      {loading ? (
        <div className="folder-tree-loading"><Spin size="small" /></div>
      ) : (
        <Tree
          key={`${version}-${folders.length}`}
          showIcon
          blockNode
          autoExpandParent={false}
          treeData={treeData}
          expandedKeys={expandedKeys}
          selectedKeys={selected ? [selected] : [MAIN_ROOT_KEY]}
          onExpand={(keys) => {
            const next = visibleExpandedKeys(folders, keys.map(String))
            rememberedExpansion = next
            hasRememberedExpansion = true
            setExpandedKeys(next)
          }}
          onSelect={(keys, info) => {
            const key = String(keys.length > 0 ? keys[0] : info.node.key)
            onSelect(key === MAIN_ROOT_KEY ? '' : key)
          }}
          onClick={(event, node) => {
            // 原生 dblclick 在快速连击时会并入同一多击序列，按偶数 click 配对才能连续切换。
            if (event.detail < 2 || event.detail % 2 !== 0) return
            const key = String(node.key)
            if (key === MAIN_ROOT_KEY) {
              onSelect('')
              return
            }
            if (!folders.some((folder) => folder.parent_id === key)) return
            toggleExpanded(key)
          }}
          onRightClick={({ event, node }) => {
            if (node.key === MAIN_ROOT_KEY) {
              event.preventDefault()
              setMenu({
                x: event.clientX,
                y: event.clientY,
                items: rootMenuItems(),
                onSelect: onAreaAction,
              })
              return
            }
            const folder = folders.find((item) => item.id === node.key)
            if (folder) openNodeMenu(event, folder)
          }}
        />
      )}
      <ContextMenu state={menu} onClose={() => setMenu(null)} />
    </div>
  )
}
