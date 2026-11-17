import { useEffect, useMemo, useRef, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import { Empty, Spin, Tooltip, Tree } from 'antd'
import type { MenuProps } from 'antd'
import {
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  FolderAddOutlined,
  FolderFilled,
  HomeFilled,
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

let rememberedExpansion: string[] = []
let hasRememberedExpansion = false

function visibleExpandedKeys(folders: NodeItem[], keys: string[]): string[] {
  const byId = new Map(folders.map((folder) => [folder.id, folder]))
  const expanded = new Set(keys)
  return keys.filter((key) => {
    let node = byId.get(key)
    if (!node) return false
    while (node && node.parent_id) {
      if (!expanded.has(node.parent_id)) return false
      node = byId.get(node.parent_id)
    }
    return true
  })
}

export function FolderTree({
  selected,
  version,
  canEdit,
  onSelect,
  onNewProject,
  onNodeAction,
  onAreaAction,
}: {
  selected: string
  version: number
  canEdit: boolean
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

  useEffect(() => {
    if (!selected) {
      followedSelection.current = null
      return
    }
    if (followedSelection.current === selected) return
    const byId = new Map(folders.map((folder) => [folder.id, folder]))
    if (!byId.has(selected)) return
    const ancestors: string[] = []
    let node = byId.get(selected)
    while (node && node.parent_id) {
      ancestors.push(node.parent_id)
      node = byId.get(node.parent_id)
    }
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

  const treeData = useMemo<DataNode[]>(() => {    const byParent = new Map<string, NodeItem[]>()
    folders.forEach((folder) => {
      const list = byParent.get(folder.parent_id) || []
      list.push(folder)
      byParent.set(folder.parent_id, list)
    })
    const build = (parent: string, depth: number): DataNode[] =>
      (byParent.get(parent) || []).map((folder) => {
        const isProject = depth === 0
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
          children: build(folder.id, depth + 1),
        }
      })
    return build('', 0)
  }, [folders])

  function nodeMenuItems(node: NodeItem): MenuProps['items'] {
    const items: MenuProps['items'] = []
    if (canEdit) {
      items.push({ key: 'new-folder', label: '新建文件夹', icon: <FolderAddOutlined /> })
    }
    items.push({ key: 'download', label: '下载文件夹（ZIP）', icon: <DownloadOutlined /> })
    if (canEdit) {
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
        if (target.closest('.ant-tree-treenode') || target.closest('.folder-tree-root')) return
        openAreaMenu(event)
      }}
    >
      <div className="folder-tree-title">文件目录</div>
      <div
        className={`folder-tree-root${selected ? '' : ' selected'}`}
        onContextMenu={openAreaMenu}
      >
        <button type="button" className="folder-tree-root-main" onClick={() => onSelect('')}>
          <HomeFilled style={{ color: '#3a79bc' }} />
          <span>全部项目</span>
        </button>
        {canEdit && (
          <Tooltip title="新建项目">
            <button type="button" className="folder-tree-root-add" onClick={onNewProject}>
              <PlusOutlined />
            </button>
          </Tooltip>
        )}
      </div>
      {loading ? (
        <div className="folder-tree-loading"><Spin size="small" /></div>
      ) : folders.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有项目" />
      ) : null}
      <Tree
        key={`${version}-${folders.length}`}
        showIcon
        blockNode
        autoExpandParent={false}
        treeData={treeData}
        expandedKeys={expandedKeys}
        selectedKeys={selected ? [selected] : []}
        onExpand={(keys) => {
          const next = visibleExpandedKeys(folders, keys.map(String))
          rememberedExpansion = next
          hasRememberedExpansion = true
          setExpandedKeys(next)
        }}
        onSelect={(keys, info) => {
          const key = String(keys.length > 0 ? keys[0] : info.node.key)
          if (key === selected && folders.some((folder) => folder.parent_id === key)) {
            const raw = expandedKeys.includes(key)
              ? expandedKeys.filter((item) => item !== key)
              : [...expandedKeys, key]
            const next = visibleExpandedKeys(folders, raw)
            rememberedExpansion = next
            hasRememberedExpansion = true
            setExpandedKeys(next)
            return
          }
          onSelect(key)
        }}
        onRightClick={({ event, node }) => {
          const folder = folders.find((item) => item.id === node.key)
          if (folder) openNodeMenu(event, folder)
        }}
      />
      <ContextMenu state={menu} onClose={() => setMenu(null)} />
    </div>
  )
}
