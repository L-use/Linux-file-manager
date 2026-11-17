import {icon} from './icons.js';
import {language,t} from './i18n.js';

const byId=id=>document.getElementById(id);
const refreshMs=100;

function uptimeLabel(seconds) {
  if (seconds==null) return '—';
  const elapsed=Math.max(0,Math.floor(seconds));
  const clock=[Math.floor(elapsed/3600)%24,Math.floor(elapsed/60)%60,elapsed%60]
    .map(value=>String(value).padStart(2,'0')).join(':');
  const days=Math.floor(elapsed/86400);
  return days?t('serverDays',{days,time:clock}):clock;
}

export function createServerStatus(read,size) {
  const summary=byId('server-summary'), overlay=byId('server-overlay');
  byId('server-status-icon').innerHTML=icon('server');
  let controller, snapshot, servicesKey, latency;
  const updateText=(node,value)=>{
    if (node.textContent!==value) node.textContent=value;
  };

  function renderServices(services) {
    const heading=byId('services-title'), list=byId('services-body');
    heading.dataset.i18n=services?.kind==='processes'?'serverProcesses':'serverServices';
    updateText(heading,t(heading.dataset.i18n));
    const visible=services?.items.slice(0,12) || [];
    updateText(byId('services-count'),services?t('serverServicesCount',{shown:visible.length,total:services.total}):'');
    const rows=visible.map(item=>[item.name,size(item.memory),uptimeLabel(item.uptime)]);
    const table=rows.length?[[t('name'),t('serverRSS'),t('serverUptime')],...rows]:null;
    const content=table || t(services?'serverNoServices':'serverUnavailable');
    const key=JSON.stringify(content);
    if (key===servicesKey) return;
    servicesKey=key;
    if (!table) {list.textContent=content;return;}
    const fragment=document.createDocumentFragment();
    for (const [index,values] of table.entries()) {
      const row=document.createElement('div');
      row.className='server-service-row'+(index===0?' server-service-header':'');
      for (const value of values) {
        const cell=document.createElement('span');
        cell.textContent=cell.title=value;
        row.append(cell);
      }
      fragment.append(row);
    }
    list.replaceChildren(fragment);
  }
  function render() {
    const number=new Intl.NumberFormat(language==='zh'?'zh-CN':'en',{style:'percent',maximumFractionDigits:1});
    for (const [key,label] of [['memory','serverMemory'],['disk','serverDisk']]) {
      const usage=snapshot?.[key];
      const fraction=usage?.total?Math.min(1,usage.used/usage.total):null;
      const formatted=fraction===null?'—':number.format(fraction);
      updateText(byId('live-'+key),t(label)+' '+formatted);
      if (!overlay.hidden) {
        const inner=byId('overlay-'+key);
        updateText(inner,formatted);
        inner.parentElement.style.setProperty('--server-fill',((fraction??0)*100)+'%');
        updateText(byId(key+'-amount'),usage?size(usage.used)+' / '+size(usage.total):t('serverUnavailable'));
      }
    }
    const network=t('serverNetwork')+' '+(latency==null?t('serverOffline'):Math.round(latency)+' ms');
    updateText(byId('live-network'),network);
    updateText(byId('overlay-network'),network);
    byId('live-network').classList.toggle('network-offline',latency==null);
    const uptime=uptimeLabel(snapshot?.uptime);
    updateText(byId('live-uptime'),t('serverUptime')+' '+uptime);
    if (overlay.hidden) return;
    updateText(byId('overlay-uptime'),uptime);
    renderServices(snapshot?.services);
  }
  function show(open) {
    summary.setAttribute('aria-expanded',String(open));
    overlay.hidden=!open;
    if (open) render();
  }
  summary.onpointerenter=event=>{if (event.pointerType!=='touch') show(true);};
  summary.onpointerleave=event=>{if (event.pointerType!=='touch') show(false);};
  summary.onclick=event=>{
    if (event.pointerType==='touch' || event.detail===0) show(overlay.hidden);
  };
  summary.onblur=()=>show(false);
  document.addEventListener('pointerdown',event=>{
    if (!overlay.hidden && !summary.contains(event.target)) show(false);
  });
  document.addEventListener('keydown',event=>{if (event.key==='Escape') show(false);});

  function stop() {
    controller?.abort();
    controller=null;
    snapshot=null;
    latency=null;
    show(false);
  }
  function start() {
    stop();
    const activity=new AbortController();
    controller=activity;
    const {signal}=activity;
    async function poll() {
      if (signal.aborted) return;
      const began=performance.now();
      if (!document.hidden) {
        const expanded=!overlay.hidden;
        try {
          const next=await read('/api/server-status'+(expanded?'?services=1':''),{signal});
          if (signal.aborted) return;
          snapshot=next;
          latency=performance.now()-began;
        } catch(error) {
          if (signal.aborted) return;
          snapshot=null;
          latency=null;
          render();
          if (error?.error==='unauthenticated') return;
        }
        render();
      }
      setTimeout(poll,Math.max(0,refreshMs-(performance.now()-began)));
    }
    poll();
  }
  return {start,stop,render};
}
