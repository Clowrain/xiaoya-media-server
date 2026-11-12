import { useEffect, useState } from 'react';
import { ChevronLeft, ChevronRight, RefreshCw, Search } from 'lucide-react';
import { notifyToast } from '../../api/client';
import { waitForAdminJob } from '../../api/jobs';
import {
  fetchResourceBatches,
  fetchResourceChanges,
  fetchResourceDetail,
  fetchResources,
  fetchResourceTasks,
  fetchTrackingReviews,
  performResourceAction,
  runResourceTask,
} from '../../api/resources';
import type {
  ManagedResource,
  ManagedResourceDetail,
  ResourceAction,
  ResourceChange,
  ResourceTask,
  TrackingReview,
} from '../../api/resources';
import type { SyncBatch } from '../../types';

const button = 'rounded-lg border border-zinc-700 bg-zinc-800 px-3 py-1.5 text-sm hover:bg-zinc-700 disabled:opacity-50';
const tabs = { resources: '资源', changes: '变更历史', batches: '扫描批次', tasks: '定时任务' };
const statuses: Record<string, string> = {
  ACTIVE: '有效', MISSING: '源端缺失', FAILED: '失败', IGNORED: '已忽略',
  SUCCESS: '成功', PARTIAL: '部分失败', INTERRUPTED: '已中断', RUNNING: '运行中', IDLE: '待执行',
};
const time = (seconds?: number) => seconds ? new Date(seconds * 1000).toLocaleString() : '—';

export function AdminResources() {
  const [tab, setTab] = useState<keyof typeof tabs>('resources');
  const [resources, setResources] = useState<ManagedResource[]>([]);
  const [detail, setDetail] = useState<ManagedResourceDetail | null>(null);
  const [changes, setChanges] = useState<ResourceChange[]>([]);
  const [batches, setBatches] = useState<SyncBatch[]>([]);
  const [tasks, setTasks] = useState<ResourceTask[]>([]);
  const [reviews, setReviews] = useState<TrackingReview[]>([]);
  const [keyword, setKeyword] = useState('');
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState('');
  const [page, setPage] = useState(0);
  const [total, setTotal] = useState(0);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const size = 25;

  const load = async () => {
    setError('');
    try {
      switch (tab) {
        case 'resources': {
          const [resourcePage, trackingReviews] = await Promise.all([
            fetchResources({ q: query, status: filter, limit: size, offset: page * size }),
            fetchTrackingReviews(),
          ]);
          setResources(resourcePage.items); setTotal(resourcePage.total); setReviews(trackingReviews);
          break;
        }
        case 'changes': setChanges(await fetchResourceChanges()); break;
        case 'batches': setBatches(await fetchResourceBatches()); break;
        case 'tasks': setTasks(await fetchResourceTasks()); break;
      }
    } catch (e: any) { setError(e.message || '无法读取资源与任务'); }
  };

  useEffect(() => { load(); }, [tab, query, filter, page]);

  const open = async (id: string) => {
    setBusy(id);
    try { setDetail(await fetchResourceDetail(id)); }
    catch (e: any) { notifyToast(e.message, 'error'); }
    finally { setBusy(''); }
  };

  const action = async (id: string, mode: ResourceAction) => {
    setBusy(id);
    try {
      await performResourceAction(id, mode);
      const message = { unmanage: '已取消管理', 'restore-management': '已恢复管理', reprocess: '已重新生成', retry: '重试完成', track: '已启用追更' };
      notifyToast(message[mode], 'success');
      if (detail?.resource.id === id) setDetail(await fetchResourceDetail(id));
      await load();
    } catch (e: any) { notifyToast(e.message, 'error'); }
    finally { setBusy(''); }
  };

  const runTask = async (key: string) => {
    setBusy(key);
    try {
      const job = await runResourceTask(key);
      const result = await waitForAdminJob(job.id);
      notifyToast(result.status === 'SUCCESS' ? '任务完成' : result.error || statuses[result.status], result.status === 'SUCCESS' ? 'success' : 'error');
      await load();
    } catch (e: any) { notifyToast(e.message, 'error'); }
    finally { setBusy(''); }
  };

  return <section className="space-y-4 rounded-2xl border border-zinc-800 bg-zinc-900/60 p-5">
    <header className="flex flex-wrap items-center justify-between gap-3">
      <h2 className="text-lg font-semibold">资源与任务</h2>
      <button type="button" className={button} onClick={load}><RefreshCw size={15} className="mr-2 inline" />刷新</button>
    </header>
    <nav className="flex flex-wrap gap-2" aria-label="资源与任务视图">
      {(Object.entries(tabs) as [keyof typeof tabs, string][]).map(([key, label]) =>
        <button type="button" key={key} onClick={() => { setDetail(null); setTab(key); }} aria-pressed={tab === key}
          className={button + (tab === key ? ' border-emerald-600 text-emerald-300' : '')}>{label}</button>
      )}
    </nav>
    {error && <p role="alert" className="text-sm text-rose-300">{error}</p>}

    {tab === 'resources' && <>
      {reviews.length > 0 && <aside className="rounded-xl border border-amber-800 bg-amber-950/30 p-4 text-sm">
        <h3 className="font-semibold text-amber-300">需要确认的旧追更目录</h3>
        <p className="mt-1 text-zinc-300">这些目录位于已停用的旧入库目录下，已暂停追更。</p>
        {reviews.map(r => <div key={r.resource_id} className="mt-3 flex flex-wrap items-center justify-between gap-2">
          <span className="break-all">{r.path}</span><button className={button} disabled={!!busy} onClick={() => action(r.resource_id, 'track')}>启用追更</button>
        </div>)}
      </aside>}
      <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); setPage(0); setQuery(keyword.trim()); }}>
        <input className="min-w-40 flex-1 rounded-lg border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm" value={keyword} onChange={e => setKeyword(e.target.value)} placeholder="按来源路径查找" aria-label="来源路径" />
        <select value={filter} onChange={e => { setFilter(e.target.value); setPage(0); }} aria-label="资源状态" className="rounded-lg border border-zinc-700 bg-zinc-950 px-3 py-2 text-sm">
          <option value="">全部状态</option><option value="ACTIVE">有效</option><option value="MISSING">源端缺失</option><option value="FAILED">处理失败</option>
        </select>
        <button className={button}><Search size={15} className="mr-1 inline" />搜索</button>
      </form>

      {detail && <section className="rounded-xl border border-zinc-700 bg-zinc-950 p-4 text-sm">
        <div className="flex flex-wrap justify-between gap-3">
          <h3 className="break-all font-semibold">{detail.resource.source_path}</h3>
          <button className={button} onClick={() => setDetail(null)}>关闭详情</button>
        </div>
        <p className="mt-2 break-all text-zinc-400">{detail.resource.local_strm_path || '尚未生成本地文件'}</p>
        {detail.resource.last_error && <p className="mt-2 break-all text-rose-300">{detail.resource.last_error}</p>}
        <div className="mt-3 flex flex-wrap gap-2">
          {detail.cleanup_preview.unmanaged
            ? <button className={button} disabled={!!busy} onClick={() => action(detail.resource.id, 'restore-management')}>恢复管理</button>
            : <>
              {!detail.resource.is_dir && detail.resource.status !== 'MISSING' &&
                <button className={button} disabled={!!busy} onClick={() => action(detail.resource.id, detail.resource.status === 'FAILED' ? 'retry' : 'reprocess')}>
                  {detail.resource.status === 'FAILED' ? '重试' : '重新生成'}
                </button>}
              <button className={button} disabled={!!busy} onClick={() => {
                if (confirm(`停止管理 ${detail.cleanup_preview.affected_resources} 项资源？本地文件、媒体条目和观看记录都会保留。`)) {
                  action(detail.resource.id, 'unmanage');
                }
              }}>取消管理</button>
            </>}
        </div>
      </section>}

      <div className="divide-y divide-zinc-800">
        {resources.map(r => <div key={r.id} className="flex items-start justify-between gap-4 py-3 text-sm">
          <div className="min-w-0"><p className="break-all font-medium">{r.source_path}</p>
            <p className="mt-1 text-zinc-400">{r.origin_type === 'ALIST_MANUAL' ? 'AList' : '小雅 SQLite'} · {statuses[r.status] ?? r.status}</p>
          </div>
          <button className={button + ' shrink-0'} disabled={!!busy} onClick={() => open(r.id)}>详情</button>
        </div>)}
        {!resources.length && <p className="py-4 text-sm text-zinc-400">没有匹配的资源。</p>}
      </div>
      <footer className="flex items-center justify-between text-sm text-zinc-400">
        <span>共 {total} 项</span>
        <div className="flex gap-2">
          <button className={button} disabled={page === 0} onClick={() => setPage(page - 1)} aria-label="上一页"><ChevronLeft size={16}/></button>
          <button className={button} disabled={(page + 1) * size >= total} onClick={() => setPage(page + 1)} aria-label="下一页"><ChevronRight size={16}/></button>
        </div>
      </footer>
    </>}

    {tab === 'changes' && <div className="divide-y divide-zinc-800">
      {changes.map(c => <div className="py-3 text-sm" key={c.id}>
        <p className="break-all">{c.new_path || c.old_path}</p>
        <p className="text-zinc-400">{({BASELINE_ADDED:'基线发现',ADDED:'新增',MODIFIED:'变更',RENAMED:'改名',MOVED:'移动',MISSING:'源端缺失',RESTORED:'恢复',MANUAL_IMPORTED:'人工入库'} as Record<string,string>)[c.change_type] ?? c.change_type} · {time(c.detected_at)}</p>
      </div>)}
      {!changes.length && <p className="text-sm text-zinc-400">暂无资源变更。</p>}
    </div>}

    {tab === 'batches' && <div className="divide-y divide-zinc-800">
      {batches.map(b => <div className="py-3 text-sm" key={b.id}>
        <p className="break-all">{b.root_path} <span className="ml-2 text-zinc-400">{statuses[b.status] ?? b.status}</span></p>
        <p className="text-zinc-400">{time(b.started_at)} · 发现 {b.total_seen} · 新增 {b.added_count} · 缺失 {b.missing_count} · 恢复 {b.restored_count} · 失败 {b.failed_count}</p>
        {b.error_message && <p className="break-all text-rose-300">{b.error_message}</p>}
      </div>)}
      {!batches.length && <p className="text-sm text-zinc-400">暂无扫描批次。</p>}
    </div>}

    {tab === 'tasks' && <div className="divide-y divide-zinc-800">
      {tasks.map(t => <div className="flex items-start justify-between gap-3 py-3 text-sm" key={t.task_key}>
        <div><p>{t.name}</p><p className="text-zinc-400">{t.enabled ? statuses[t.status] ?? t.status : '自动运行已停用'} · 最近运行 {time(t.last_run_at)}</p>
          {t.last_error && <p className="break-all text-rose-300">{t.last_error}</p>}
        </div>
        <button className={button + ' shrink-0'} disabled={!!busy || t.status === 'RUNNING'} onClick={() => runTask(t.task_key)}>{busy === t.task_key ? '运行中' : '立即运行'}</button>
      </div>)}
    </div>}
  </section>;
}
