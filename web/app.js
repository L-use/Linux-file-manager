import {icon} from './icons.js';
import {language, setLanguage, t, translate} from './i18n.js';
import {mountPreview, fileType, mountGridThumbnails} from './preview.js';
import {createServerStatus} from './server-status.js';

const $ = (id) => document.getElementById(id);
const tree = $('tree'), workspace = $('workspace'), search = $('search'), menu = $('menu'), dialog = $('dialog');
const cache = new Map(), expanded = new Set(['']), directoryErrors = new Map();
const editorPane=$('editor');
let hiddenFolders=new Set(), showHidden=false;
const treeSortModes={
  sortName:['name','sort',1],
  sortSizeAsc:['size','ascending',1], sortSizeDesc:['size','descending',-1],
  sortDateAsc:['modified','ascending',1], sortDateDesc:['modified','descending',-1],
};
const treeNames=new Intl.Collator(undefined,{numeric:true,sensitivity:'base'});
let treeSort='sortName';
try {
  const saved=localStorage.getItem('fm-tree-sort');
  if (Object.hasOwn(treeSortModes,saved)) treeSort=saved;
} catch {}
let root = '', selected = '', current = null, activePreview = null, previewId = 0, generation = 0;
let sessionUser='', loginError = '', uploadState = null, uploadResult = null, popup = null, modal = null, signedIn = false;
let gridPath=null, selecting=false, bulkBusy=false, gridPreview;
const gridThumbnails=new Map();
const marked=new Map();

const parent = (name) => name.includes('/') ? name.slice(0,name.lastIndexOf('/')) : '';
const join = (directory,name) => directory ? directory + '/' + name : name;
const absolute = (path) => path ? root.replace(/\/$/,'') + '/' + path : root;
const query = (route,path,extra={}) => '/api/' + route + '?' + new URLSearchParams({path,...extra});
const fileURL = (path,download=false) => query('file',path,download ? {download:'1'} : {});
const errorOf = (error) => t(error?.error || 'network');
const cleanName = (name) => name && name !== '.' && name !== '..' && !name.includes('/');
const folders = () => [...cache.values()].flat().filter(e => e.kind === 'directory').map(e => e.path);
const rootEntry = () => ({name:root,path:'',kind:'directory'});

function size(bytes) {
  if (bytes < 1024) return bytes + ' B';
  const units = ['KiB','MiB','GiB','TiB','PiB'];
  let unit=0, amount=bytes/1024;
  while (amount >= 1024 && unit < units.length-1) {amount/=1024;unit++;}
  return new Intl.NumberFormat(language==='zh'?'zh-CN':'en',{maximumFractionDigits:1}).format(amount) + ' ' + units[unit];
}

async function api(route, options = {}) {
  const opts = {...options,headers:{...options.headers}};
  if (opts.method) opts.headers['X-FM-Request']='1';
  if (opts.body && typeof opts.body !== 'string') {
    opts.body=JSON.stringify(opts.body);
    opts.headers['Content-Type']='application/json';
  }
  let reply;
  try {reply=await fetch(route,opts);} catch(error) { if (error.name==='AbortError') throw error; throw {error:'network'}; }
  if (reply.ok) return reply;
  const detail=await reply.json().catch(()=>({error:reply.headers.get('X-FM-Error') || 'io'}));
  if (reply.status===401 && signedIn && !route.endsWith('/login') && !unsaved()) showLogin('unauthenticated');
  throw detail;
}
async function json(route, options) { return (await api(route,options)).json(); }
const action = (body) => json('/api/action',{method:'POST',body});
const liveStatus=createServerStatus(json,size);
function bytesPercent(bytes,total,complete=false) {
  return total>0?Math.min(1,bytes/total):Number(complete);
}
function meterText(bytes,total,rate,complete=false) {
  const fraction=bytesPercent(bytes,total,complete);
  const progress=new Intl.NumberFormat(language==='zh'?'zh-CN':'en',{
    style:'percent',maximumFractionDigits:1,
  }).format(fraction<1?Math.floor(fraction*1000)/1000:1);
  return [progress,size(bytes)+' / '+size(total),size(Math.round(rate))+'/s'].join(' · ');
}
function sampleRate(meter,bytes) {
  const now=performance.now();
  const elapsed=now-meter.time;
  if (bytes<meter.bytes) {Object.assign(meter,{time:now,bytes,rate:0});return;}
  if (elapsed<100) return;
  const speed=(bytes-meter.bytes)*1000/elapsed;
  meter.rate=meter.rate && speed?(meter.rate+speed)/2:speed;
  meter.time=now;
  meter.bytes=bytes;
}
const newMeter=()=>({time:performance.now(),bytes:0,rate:0});
function download(entry) {
  const link=document.createElement('a');
  link.href=fileURL(entry.path,true);
  link.download=entry.name;
  document.body.append(link);
  link.click();
  link.remove();
}

function makeButton(label,glyph,callback,className='icon-button',text=false) {
  const b=document.createElement('button');
  b.type='button';
  b.className=className;
  b.dataset.label=label;
  b.innerHTML=icon(glyph);
  if (text) {
    const span=document.createElement('span');
    span.dataset.i18n=label;
    b.append(span);
  }
  b.onclick=callback;
  return b;
}
function toast(key,values,error=false,shortcut) {
  const box=document.createElement('div');
  box.className='toast'+(error?' error':'');
  box.innerHTML=icon(error?'alert':'check');
  const message=document.createElement('span');
  const record={key,values};
  const display=()=>t(record.key,record.values);
  message.textContent=display();
  box.append(message);
  if (shortcut) {
    box.dataset.trashUndo=shortcut.id;
    const button=makeButton(shortcut.label,shortcut.icon,()=>{
      box.remove();
      shortcut.run();
    },'button',true);
    box.append(button);
    translate(box);
  }
  box.translateMessage=()=>{message.textContent=display();};
  $('toasts').append(box);
  setTimeout(()=>box.remove(),shortcut?8000:4500);
}
const tell = (e) => toast(e?.error || 'network',{},true);
const unsaved = () => Boolean(activePreview?.dirty?.());
async function reviewEdits() {
  const preview=activePreview;
  if (!preview?.dirty?.()) return 'clean';
  if (dialog.open || preview.saving?.()) return null;
  const choice=await promptDialog({
    title:'unsavedTitle',message:'unsavedPrompt',values:{name:current.name},
    submit:'saveContinue',alt:'discardEdits',
  });
  if (!choice || preview!==activePreview) return null;
  if (choice==='discardEdits') return 'discard';
  return await preview.save() ? 'saved' : null;
}
const containsPath = (directory,path) => path===directory || path.startsWith(directory+'/');

const visibilityKey=()=> 'fm-hidden-folders:'+root;
function loadVisibility() {
  showHidden=false;
  try {
    const values=JSON.parse(localStorage.getItem(visibilityKey()) || '[]');
    hiddenFolders=new Set(Array.isArray(values)?values.filter(value=>typeof value==='string' && value):[]);
  } catch {hiddenFolders=new Set();}
  updateHiddenButton();
}
function saveVisibility() {
  try {localStorage.setItem(visibilityKey(),JSON.stringify([...hiddenFolders]));} catch {}
}
function isVisiblePath(path) {
  if (showHidden) return true;
  for (let prefix=path;prefix;prefix=parent(prefix)) {
    if (hiddenFolders.has(prefix)) return false;
  }
  return true;
}
function updateHiddenButton() {
  const button=$('show-hidden');
  button.innerHTML=icon(showHidden?'eyeOff':'eye');
  button.setAttribute('aria-pressed',String(showHidden));
}
function revealHidden(enabled) {
  showHidden=enabled;
  updateHiddenButton();
  while (selected && !isVisiblePath(selected)) selected=parent(selected);
  renderTree();
}
function moveHiddenPaths(source,destination) {
  let changed=false;
  for (const hidden of [...hiddenFolders]) {
    if (!containsPath(source,hidden)) continue;
    hiddenFolders.delete(hidden);
    hiddenFolders.add(destination+hidden.slice(source.length));
    changed=true;
  }
  if (changed) saveVisibility();
}

const isPreviewFullscreen=()=>document.fullscreenElement===editorPane;
function renderFullscreenButton() {
  const button=$('preview-fullscreen');
  button.dataset.label=isPreviewFullscreen()?'exitFullscreen':'fullscreen';
  button.setAttribute('aria-pressed',String(isPreviewFullscreen()));
  button.title=t(button.dataset.label);
  button.setAttribute('aria-label',button.title);
}
function leavePreviewFullscreen() {
  if (isPreviewFullscreen()) document.exitFullscreen().catch(()=>{});
}
async function fullscreenPreview() {
  if (!current) return;
  if (isPreviewFullscreen()) {leavePreviewFullscreen();return;}
  try {
    await editorPane.requestFullscreen();
    activePreview?.prepareFullscreen?.();
  } catch {
    toast('fullscreenUnavailable',{},true);
  }
}

function setDrawer(open) {
  workspace.classList.toggle('drawer',open);
  $('toggle-tree').setAttribute('aria-expanded',String(open));
  $('backdrop').hidden=!open;
  if (open) search.focus();
}

function showLogin(error='') {
  signedIn=false;
  gridPath=null;
  gridPreview?.abort();
  gridThumbnails.clear();
  selecting=false;
  marked.clear();
  $('settings').hidden=false;
  $('settings').classList.remove('update-available');
  sessionUser='';
  stopUpdateReminder();
  updateGridButton();
  liveStatus.stop();
  cancelUpload();
  uploadResult=null;
  paintUpload();
  leavePreviewFullscreen();
  if ($('tools-dialog').open) $('tools-dialog').close();
  generation++;
  workspace.hidden=true;
  $('boot').hidden=true;
  $('toggle-tree').disabled=true;
  $('login-screen').hidden=false;
  loginError=error;
  $('login-error').textContent=error?t(error):'';
  closeMenu(false);
  if (dialog.open) dialog.close();
  setDrawer(false);
  activePreview?.destroy();
  activePreview=null;
  current=null;
  selected='';
  previewId++;
  $('preview-host').replaceChildren();
  cache.clear();
  expanded.clear();
  expanded.add('');
  directoryErrors.clear();
  search.value='';
  $('login-form').elements.password.value='';
  if (error) $('login-form').elements.password.focus();
}
async function showWorkspace(session) {
  signedIn=true;
  root=session.root;
  loadVisibility();
  $('login-screen').hidden=true;
  $('boot').hidden=true;
  workspace.hidden=false;
  $('settings').hidden=false;
  startUpdateReminder();
  $('toggle-tree').disabled=false;
  sessionUser=session.user;
  if ($('tools-dialog').open) $('tools-dialog').close();
  setEmpty('emptyTitle','emptyHint');
  renderHeader();
  liveStatus.start();
  renderTree();
  await loadDirectory('');
  renderTree();
}

async function loadDirectory(path) {
  const session=generation;
  directoryErrors.delete(path);
  try {
    const list=await json(query('list',path));
    if (session===generation) cache.set(path,list);
  } catch(error) {
    if (session===generation) {
      directoryErrors.set(path,error?.error || 'network');
      cache.delete(path);
    }
  }
}
function directoryNote(key,depth) {
  const note=document.createElement('div');
  note.className='tree-note';
  note.style.setProperty('--depth',depth);
  note.textContent=t(key);
  return note;
}
function orderChildren(children) {
  const [field,,direction]=treeSortModes[treeSort];
  if (field==='name') return children;
  return children.sort((a,b)=>{
    const folderOrder=Number(b.kind==='directory')-Number(a.kind==='directory');
    if (folderOrder) return folderOrder;
    if (field==='size') {
      const fileOrder=Number(b.kind==='file')-Number(a.kind==='file');
      if (fileOrder) return fileOrder;
    }
    const difference=field==='size' && a.kind!=='file'?0:direction*(a[field]-b[field]);
    return difference || treeNames.compare(a.name,b.name) || (a.name<b.name?-1:Number(a.name>b.name));
  });
}
function openTreeSort() {
  const button=$('tree-sort');
  if (popup?.anchor===button) {closeMenu();return;}
  const items=Object.entries(treeSortModes).map(([key,[,glyph]])=>[
    key,glyph,()=>{
      treeSort=key;
      try {localStorage.setItem('fm-tree-sort',key);} catch {}
      renderTree();
    },key===treeSort,
  ]);
  openMenu(items,button);
}
function renderTree(focusPath) {
  const currentFocus=document.activeElement.closest?.('.tree-row');
  const focus=focusPath ?? currentFocus?.dataset.path;
  const focusMenu=document.activeElement.classList.contains('tree-more');
  const fragment=document.createDocumentFragment();
  const count=[...cache.values()].reduce((sum,list)=>sum+list.filter(entry=>isVisiblePath(entry.path)).length,0);
  const filter=search.value.trim().toLocaleLowerCase();
  const matchMemo=new Map();
  function matches(entry) {
    if (!isVisiblePath(entry.path)) return false;
    if (!filter) return true;
    if (!matchMemo.has(entry.path)) matchMemo.set(entry.path,entry.name.toLocaleLowerCase().includes(filter) || (cache.get(entry.path)||[]).some(matches));
    return matchMemo.get(entry.path);
  }
  let newFocus;
  function row(entry,depth,isRoot=false) {
    const isDir=entry.kind==='directory', open=expanded.has(entry.path) || (Boolean(filter) && cache.has(entry.path));
    const node=document.createElement('div');
    node.className='tree-row'+(isRoot?' root-row':'')+(selected===entry.path?' selected':'');
    node.dataset.path=entry.path;
    node.classList.toggle('hidden-folder',isDir && hiddenFolders.has(entry.path));
    const button=document.createElement('button');
    button.type='button';
    button.className='tree-main';
    button.style.setProperty('--depth',Math.min(depth,15));
    if (isDir) button.setAttribute('aria-expanded',String(open));
    if (selected===entry.path) button.setAttribute('aria-current','true');
    button.title=isRoot?root:entry.name;
    const type=fileType(entry);
    button.innerHTML='<span class="arrow">'+(isDir?icon(open?'down':'chevron'):'')+'</span><span class="type-'+type.style+'">'+icon(isDir&&open?'folderOpen':type.icon)+'</span>';
    const label=document.createElement('span');
    label.className='label';
    label.textContent=entry.name;
    button.append(label);
    if (isDir && hiddenFolders.has(entry.path)) {
      const marker=document.createElement('span');
      marker.className='hidden-folder-marker';
      marker.innerHTML=icon('eyeOff');
      marker.dataset.label='folderHidden';
      button.append(marker);
    }
    button.onclick=()=>isDir?toggleDirectory(entry):showFile(entry);
    button.onkeydown=(e)=>{
      if (e.key==='ContextMenu' || (e.shiftKey && e.key==='F10')) {e.preventDefault(); itemMenu(entry,button); }
      if (e.key==='ArrowLeft' && isDir && expanded.has(entry.path)) {e.preventDefault(); toggleDirectory(entry);}
      if (e.key==='ArrowRight' && isDir && !expanded.has(entry.path)) {e.preventDefault(); toggleDirectory(entry);}
      if (e.key==='ArrowUp' || e.key==='ArrowDown') {
        e.preventDefault();
        const neighbor=e.key==='ArrowUp'?node.previousElementSibling:node.nextElementSibling;
        neighbor?.querySelector('.tree-main')?.focus();
      }
    };
    if (selecting && !isRoot) node.append(selectionCheckbox(entry));
    node.append(button);
    const more=makeButton('more','more',()=>itemMenu(entry,more),'icon-button tree-more');
    more.setAttribute('aria-haspopup','menu');
    node.append(more);
    node.oncontextmenu=(e)=>{e.preventDefault(); itemMenu(entry,button,{x:e.clientX,y:e.clientY});};
    fragment.append(node);
    if (focus===entry.path) newFocus=focusMenu?more:button;
    if (!isDir || !open) return;
    const children=cache.get(entry.path)?.filter(child=>isVisiblePath(child.path));
    if (directoryErrors.has(entry.path)) fragment.append(directoryNote(directoryErrors.get(entry.path),depth+1));
    else if (!children) fragment.append(directoryNote('loading',depth+1));
    else if (!children.length && !filter) fragment.append(directoryNote('emptyFolder',depth+1));
    else orderChildren(children.filter(matches)).forEach(child=>row(child,depth+1));
  }
  row(rootEntry(),0,true);
  if (filter && cache.has('') && !cache.get('').some(matches)) fragment.append(directoryNote('noMatches',1));
  tree.replaceChildren(fragment);
  updateBulkActions();
  $('item-count').textContent=t('itemCount',{n:count});
  translate(tree);
  newFocus?.focus({preventScroll:true});
  renderGrid();
}
function selectionCheckbox(entry) {
  const box=document.createElement('input');
  box.type='checkbox';
  box.className='row-selection';
  box.checked=marked.has(entry.path);
  box.setAttribute('aria-label',t('selectItem',{name:entry.name}));
  box.onchange=()=>{
    if (box.checked) marked.set(entry.path,entry);
    else marked.delete(entry.path);
    for (const other of document.querySelectorAll('.row-selection')) {
      if (other.dataset.path===entry.path) other.checked=box.checked;
    }
    updateBulkActions();
  };
  box.dataset.path=entry.path;
  return box;
}
function updateBulkActions() {
  $('bulk-delete').hidden=!selecting;
  $('bulk-delete').disabled=!marked.size || bulkBusy;
  $('bulk-count').textContent=selecting?t('selectedItems',{count:marked.size}):'';
  $('select-many').disabled=bulkBusy;
  $('select-many').setAttribute('aria-pressed',String(selecting));
}
function updateGridButton() {
  const button=$('open-grid');
  button.setAttribute('aria-pressed',String(gridPath!==null));
}
function gridDragSelection(grid, tiles) {
  let drag;
  const rectangle=document.createElement('div');
  rectangle.className='grid-marquee';
  rectangle.setAttribute('aria-hidden','true');
  grid.onpointerdown=event=>{
    if (bulkBusy || event.button!==0 || event.pointerType!=='mouse' || event.target.closest('.tile-more,input')) return;
    drag={id:event.pointerId,x:event.clientX,y:event.clientY,origin:event.target.closest('.file-tile'),scroll:grid.scrollTop};
  };
  grid.onpointermove=event=>{
    if (!drag || drag.id!==event.pointerId || !(event.buttons&1)) return;
    if (!drag.initial) {
      if (Math.hypot(event.clientX-drag.x,event.clientY-drag.y)<6) return;
      drag.initial=new Map(marked);
      grid.setPointerCapture(event.pointerId);
      grid.append(rectangle);
    }
    const bounds=grid.getBoundingClientRect();
    const startY=drag.y+drag.scroll-grid.scrollTop;
    const left=Math.max(bounds.left,Math.min(drag.x,event.clientX));
    const right=Math.min(bounds.right,Math.max(drag.x,event.clientX)+1);
    const top=Math.max(bounds.top,Math.min(startY,event.clientY));
    const bottom=Math.min(bounds.bottom,Math.max(startY,event.clientY)+1);
    Object.assign(rectangle.style,{left:left+'px',top:top+'px',width:Math.max(0,right-left)+'px',height:Math.max(0,bottom-top)+'px'});
    for (const [tile,entry] of tiles) {
      const box=tile.getBoundingClientRect();
      const hit=tile===drag.origin || (box.left<right && box.right>left && box.top<bottom && box.bottom>top);
      const checked=hit || drag.initial.has(entry.path);
      if (checked) marked.set(entry.path,entry);
      else marked.delete(entry.path);
      tile.classList.toggle('marquee-selected',checked);
      const checkbox=tile.querySelector('.row-selection');
      if (checkbox) checkbox.checked=checked;
    }
    if (selecting) updateBulkActions();
  };
  const finish=event=>{
    if (!drag || drag.id!==event.pointerId) return;
    const initial=drag.initial;
    drag=null;
    if (!initial) return;
    if (event.type==='pointerup') {
      selecting=true;
    } else {
      marked.clear();
      for (const [path, entry] of initial) marked.set(path,entry);
    }
    renderTree();
  };
  grid.onpointerup=grid.onpointercancel=grid.onlostpointercapture=finish;
  grid.onpointerleave=()=>{if (!drag?.initial) drag=null;};
}

function renderGrid() {
  if (gridPath===null || current) return;
  updateGridButton();
  gridPreview?.abort();
  gridPreview=new AbortController();
  const host=$('preview-host');
  const prior=host.querySelector('.file-grid')?.scrollTop || 0;
  const entries=cache.get(gridPath);
  const error=directoryErrors.get(gridPath);
  if (!entries || error) {setEmpty(error||'loading','',null,!error);return;}
  const headline=document.createElement('div');
  headline.className='grid-head';
  const location=document.createElement('strong');
  location.className='truncate';
  location.textContent=location.title=absolute(gridPath);
  headline.append(location);
  if (gridPath) headline.prepend(makeButton('parentFolder','chevron',()=>openGrid(parent(gridPath))));
  const grid=document.createElement('div');
  grid.className='file-grid';
  const tiles=new Map(), previews=[];
  const filter=search.value.trim().toLocaleLowerCase();
  for (const entry of orderChildren(entries.filter(item=>isVisiblePath(item.path) && item.name.toLocaleLowerCase().includes(filter)))) {
    const tile=document.createElement('div');
    tile.className='file-tile';
    const type=fileType(entry);
    const button=document.createElement('button');
    button.type='button';
    button.className='tile-open';
    button.title=entry.name;
    const preview=document.createElement('span');
    preview.className='tile-preview type-'+type.style;
    preview.innerHTML=icon(type.icon);
    if (entry.kind==='file') previews.push({entry,type,element:preview});
    const name=document.createElement('span');
    name.className='truncate';
    name.textContent=entry.name;
    button.append(preview,name);
    button.onclick=()=>entry.kind==='directory'?openGrid(entry.path):showFile(entry);
    tile.append(button,makeButton('more','more',event=>itemMenu(entry,event.currentTarget),'icon-button tile-more'));
    if (selecting) tile.append(selectionCheckbox(entry));
    tile.oncontextmenu=event=>{event.preventDefault();itemMenu(entry,button,{x:event.clientX,y:event.clientY});};
    tiles.set(tile,entry);
    grid.append(tile);
  }
  if (!grid.children.length) grid.append(directoryNote(filter?'noMatches':'emptyFolder',0));
  else gridDragSelection(grid,tiles);
  host.replaceChildren(headline,grid);
  grid.scrollTop=prior;
  mountGridThumbnails(grid,previews,gridPreview.signal,query,fileURL,gridThumbnails);
  translate(host);
}
async function openGrid(path=targetDirectory()) {
  if (current && !await reviewEdits()) return;
  if (current) closePreview(true);
  gridPath=path;
  selected=path;
  closeMenu(false);
  setDrawer(false);
  renderTree();
  if (!cache.has(path) || directoryErrors.has(path)) {
    await loadDirectory(path);
    if (gridPath===path) renderGrid();
  }
}
async function toggleDirectory(entry) {
  const path=entry.path;
  if (gridPath!==null) {await openGrid(path);return;}
  selected=path;
  if (expanded.has(path) && !directoryErrors.has(path)) {
    expanded.delete(path);
    renderTree(path);
  } else {
    expanded.add(path);
    const need=!cache.has(path);
    renderTree(path);
    if (need) {await loadDirectory(path);renderTree();}
  }
}
async function locate(path) {
  if (!isVisiblePath(path)) revealHidden(true);
  if (matchMedia('(max-width:690px)').matches) setDrawer(true);
  search.value='';
  const parts=path?path.split('/'):[];
  let prefix='';
  for (const part of ['',...parts]) {
    prefix=part?join(prefix,part):'';
    expanded.add(prefix);
    if (!cache.has(prefix)) await loadDirectory(prefix);
  }
  selected=path;
  renderTree(path);
  document.activeElement.closest('.tree-row')?.scrollIntoView({block:'nearest'});
}
function targetDirectory() {
  const chosen=cache.get(parent(selected))?.find(entry=>entry.path===selected);
  return chosen && chosen.kind!=='directory'?parent(selected):selected;
}
async function refresh(notify=false) {
  const newest=[...new Set([...expanded,...cache.keys()])].sort((a,b)=>a.split('/').length-b.split('/').length);
  if (!newest.includes('')) newest.unshift('');
  for (const path of newest) {
    await loadDirectory(path);
    if (path && directoryErrors.get(path)==='missing') {
      expanded.delete(path);
      directoryErrors.delete(path);
      if (selected===path) selected=parent(path);
    }
  }
  for (const path of [...cache.keys()]) {
    const siblings=cache.get(parent(path));
    if (path && siblings && !siblings.some(entry=>entry.path===path && entry.kind==='directory')) {
      cache.delete(path);
      expanded.delete(path);
    }
  }
  const requested=current;
  if (requested) {
    try {
      const live=await json(query('stat',requested.path));
      if (current!==requested) return;
      if (live.kind!==requested.kind) throw {error:'changed'};
      if (live.version!==requested.version) {
        if (unsaved()) tell({error:'changed'});
        else await showFile(live,{focus:false,closeDrawer:false,changeTarget:false});
      }
    } catch(error) {
      if (current===requested && unsaved()) tell(error);
      else if (current===requested) {
        closePreview();
        if (selected===requested.path) selected=parent(requested.path);
        setEmpty(error.error || 'readError');
        tell(error);
      }
    }
  }
  while (gridPath && !cache.has(gridPath) && !directoryErrors.has(gridPath)) gridPath=parent(gridPath);
  renderTree();
  if (notify) {
    if (directoryErrors.has('')) tell({error:directoryErrors.get('')});
    else toast('refreshed');
  }
}

function setEmpty(title, description='', entry=null, loading=false) {
  const host=$('preview-host');
  const box=document.createElement('div');
  box.className='empty';
  const mark=document.createElement('div');
  mark.className=loading?'spinner':'empty-mark';
  if (!loading) mark.innerHTML=icon(title==='emptyTitle'?'file':'alert');
  const heading=document.createElement('strong');
  heading.dataset.i18n=title;
  box.append(mark,heading);
  if (description) {
    const p=document.createElement('p');
    p.dataset.i18n=description;
    box.append(p);
  }
  if (entry?.kind==='file') {
    const a=document.createElement('a');
    a.className='button download';
    a.href=fileURL(entry.path,true);
    a.download=entry.name;
    a.dataset.label='download';
    a.innerHTML=icon('download')+'<span data-i18n="download"></span>';
    box.append(a);
  }
  if (!entry && title==='emptyTitle') {
    const b=makeButton('openExplorer','explorer',()=>setDrawer(true),'button download mobile-toggle',true);
    box.append(b);
  }
  host.replaceChildren(box);
  translate(host);
}

function renderHeader() {
  const tab=$('tab'), trail=$('breadcrumb');
  tab.replaceChildren();
  trail.replaceChildren();
  $('file-actions').hidden=!current;
  $('preview-fullscreen').hidden=current?.kind!=='file';
  renderFullscreenButton();
  const n=document.createElement('span');
  n.className='tab-name truncate';
  if (current) {
    const type=fileType(current);
    const image=document.createElement('span');
    image.className='type-'+type.style;
    image.innerHTML=icon(type.icon);
    n.textContent=current.name;
    n.title=current.name;
    n.tabIndex=0;
    tab.append(image,n,makeButton('close','close',()=>closePreview()));
    $('download').hidden=current.kind!=='file';
    $('download').href=fileURL(current.path,true);
    $('download').download=current.name;
    $('file-size').textContent=current.kind==='file'?size(current.size):'';
  } else {
    n.dataset.i18n='preview';
    tab.append(n);
  }
  const rootButton=document.createElement('button');
  rootButton.type='button';
  rootButton.className='crumb';
  rootButton.title=root;
  rootButton.textContent=root;
  rootButton.onclick=()=>locate('');
  trail.append(rootButton);
  if (current) {
    const parts=current.path.split('/');
    let prefix='';
    for (const [index,part] of parts.entries()) {
      const path=prefix=join(prefix,part);
      const last=index===parts.length-1;
      const crumb=document.createElement(last?'span':'button');
      crumb.className='crumb'+(last?' current':'');
      crumb.textContent=part;
      crumb.title=last?absolute(path):part;
      if (last) crumb.tabIndex=0;
      else {crumb.type='button';crumb.onclick=()=>locate(path);}
      const separator=document.createElement('span');
      separator.className='crumb-separator';
      separator.innerHTML=icon('chevron');
      trail.append(separator,crumb);
    }
  }
  updateStatus();
  translate(tab);
}
function updateStatus(detail='') {
  $('status-details').textContent=[detail,current?.kind==='file'?size(current.size):''].filter(Boolean).join('  ·  ');
}
async function closePreview(discardApproved=false) {
  if (!discardApproved && unsaved() && !await reviewEdits()) return;
  leavePreviewFullscreen();
  previewId++;
  activePreview?.destroy();
  activePreview=null;
  current=null;
  setEmpty('emptyTitle','emptyHint');
  renderHeader();
}
async function showFile(entry, options={}) {
  if (!options.discardApproved && unsaved() && !await reviewEdits()) return;
  gridPath=null;
  gridPreview?.abort();
  updateGridButton();
  const id=++previewId;
  activePreview?.destroy();
  current=entry;
  if (options.changeTarget!==false) {
    const prior=tree.querySelector('.tree-row.selected');
    selected=entry.path;
    const row=tree.querySelector(`.tree-row[data-path="${CSS.escape(selected)}"]`);
    if (prior!==row) {
      prior?.classList.remove('selected');
      prior?.querySelector('.tree-main')?.removeAttribute('aria-current');
      row?.classList.add('selected');
      row?.querySelector('.tree-main')?.setAttribute('aria-current','true');
    }
    if (options.focus!==false) row?.querySelector('.tree-main')?.focus({preventScroll:true});
  }
  renderHeader();
  if (options.closeDrawer!==false) setDrawer(false);
  const controller=new AbortController();
  let dispose, failed=false, detail='';
  const refreshLanguage=()=>{translate($('preview-host'));dispose?.language?.();};
  activePreview={
    destroy(){controller.abort();dispose?.destroy?.();},language:refreshLanguage,
    dirty:()=>dispose?.dirty?.(),saving:()=>dispose?.saving?.(),save:()=>dispose?.save?.(),
    prepareFullscreen:()=>dispose?.prepareFullscreen?.(),
  };

  let latest=entry;
  const initialType=fileType(entry);
  const url=initialType.style==='image'&&['tiff','tif','dng'].includes(initialType.ext)?query('raster',entry.path):fileURL(entry.path);
  const alive=()=>id===previewId && !controller.signal.aborted && !failed;
  function showFailure(code) {
    if (!alive()) return;
    failed=true;
    dispose?.destroy?.();
    const unavailable=['missing','permission','protected','boundary','unauthenticated','special'].includes(code);
    setEmpty(code,'',unavailable?null:latest);
    if (code==='missing') {
      current=null;
      leavePreviewFullscreen();
      if (selected===entry.path) selected=parent(entry.path);
      renderHeader();
      renderTree();
    } else if (unavailable) $('download').hidden=true;
  }
  const context={
    host:$('preview-host'),signal:controller.signal,url,fullscreen:fullscreenPreview,encoding:options.encoding,
    showAs:format=>showFile(current,{format,encoding:options.encoding}),
    setEncoding:encoding=>showFile(current,{format:options.format,encoding}),
    getText:()=>json(query('text',entry.path,{encoding:options.encoding||'auto'}),{signal:controller.signal}),
    archive:()=>json(query('archive',entry.path),{signal:controller.signal}),
    archiveMember:index=>query('archive-member',entry.path,{index,version:latest.version}),
    slide:(page,signal=controller.signal)=>json(query('presentation',entry.path,{page}),{signal}),
    slideImage:part=>query('presentation-image',entry.path,{part}),
    saveText:(body,version)=>json(query('text',entry.path,{version}),{
      method:'PUT',body,headers:{'Content-Type':'text/plain; charset=utf-8'},signal:controller.signal,
    }),
    getWord:()=>json(query('word',entry.path),{signal:controller.signal}),
    saveWord:(body,version)=>json(query('word',entry.path,{version}),{method:'PUT',body,signal:controller.signal}),
    saveSheet:(body,version)=>json(query('sheet',entry.path,{version}),{method:'PUT',body,signal:controller.signal}),
    async saved() {
      if (!alive()) return;
      toast('saved',{name:latest.name});
      await showFile(latest,{focus:false,closeDrawer:false,changeTarget:false});
      await loadDirectory(parent(entry.path));
      renderTree();
    },
    notify:tell,
    read:()=>api(url,{signal:controller.signal}),
    async thumbnail(edge,signal) {
      const reply=await api(query('thumbnail',entry.path,{edge}),{signal});
      const width=Number(reply.headers.get('X-FM-Image-Width'));
      const height=Number(reply.headers.get('X-FM-Image-Height'));
      if (!width || !height) throw {error:'mediaError'};
      return {width,height,blob:await reply.blob()};
    },
    probeMedia:()=>api(url,{method:'HEAD',signal:controller.signal}),
    async convertVideo() {
      const route=query('compatible-video',entry.path);
      await api(route,{method:'POST',signal:controller.signal});
      return route;
    },
    report:showFailure,
    details(value){if (alive()) {detail=value;updateStatus(detail);}},
    size,button:makeButton,
  };
  const directMedia=entry.kind==='file' && options.format!=='source' && ['image','video','audio'].includes(initialType.style);
  try {
    // Media requests can start from the directory entry while metadata is checked separately.
    if (directMedia) {
      dispose=mountPreview({...context,entry,type:initialType});
      refreshLanguage();
    } else setEmpty('loading','',null,true);

    latest=await json(query('stat',entry.path),{signal:controller.signal});
    if (!alive()) return;
    current=latest;
    if (latest.kind!=='file') {renderHeader();showFailure('special');return;}
    if (directMedia) {
      if (latest.version!==entry.version) {
        renderHeader();
        updateStatus(detail);
      }
      return;
    }
    renderHeader();
    // TS files share an extension with TypeScript, so check the detected content type.
    const head=await context.probeMedia();
    if (!alive()) return;
    latest.media=head.headers.get('Content-Type');
    const detected=fileType(latest);
    const type=options.format==='source'&&detected.ext==='svg'?{...detected,style:'code'}:detected;
    if (latest.media==='video/mp2t') renderHeader();
    dispose=await mountPreview({...context,entry:latest,type});
    if (controller.signal.aborted) dispose?.destroy?.();
    else if (alive()) refreshLanguage();
  } catch (error) {
    showFailure(error?.error || 'readError');
  }
}

function closeMenu(refocus=true) {
  const prior=popup;
  popup=null;
  menu.hidden=true;
  if (prior?.anchor.id==='tree-sort') prior.anchor.setAttribute('aria-expanded','false');
  if (menu.parentElement!==document.body) document.body.append(menu);
  if (refocus && prior?.anchor.isConnected) prior.anchor.focus();
}
function openMenu(items,anchor,point) {
  if (popup) closeMenu(false);
  if (isPreviewFullscreen() && editorPane.contains(anchor)) editorPane.append(menu);
  popup={items,anchor,point};
  if (anchor.id==='tree-sort') anchor.setAttribute('aria-expanded','true');
  renderMenu();
  menu.firstElementChild?.focus();
}
function renderMenu() {
  if (!popup) return;
  const {items,anchor,point}=popup;
  menu.replaceChildren(...items.map(([label,glyph,callback,checked])=>{
    const b=makeButton(label,glyph,()=>{closeMenu();callback();},label==='delete'?'delete-menu':'',true);
    b.setAttribute('role',typeof checked==='boolean'?'menuitemradio':'menuitem');
    if (typeof checked==='boolean') {
      b.setAttribute('aria-checked',String(checked));
      if (checked) b.insertAdjacentHTML('beforeend',icon('check'));
    }
    return b;
  }));
  menu.hidden=false;
  translate(menu);
  const rect=anchor.getBoundingClientRect();
  menu.style.left=Math.max(5,Math.min(point?.x ?? rect.right-menu.offsetWidth,innerWidth-menu.offsetWidth-5))+'px';
  menu.style.top=Math.max(5,Math.min(point?.y ?? rect.bottom+3,innerHeight-menu.offsetHeight-5))+'px';
}
function itemMenu(entry,anchor,point) {
  const items=[];
  if (entry.kind==='directory') {
    items.push(['upload','upload',()=>pickUpload(entry.path)],['uploadFolder','uploadFolder',()=>pickUpload(entry.path,true)],['newFolder','newFolder',()=>createFolder(entry.path)]);
  }
  if (entry.kind==='file') items.push(['download','download',()=>download(entry)]);
  if (entry.kind==='file' && /\.zip$/i.test(entry.name)) items.push(['extractZip','archive',()=>extractZip(entry)]);
  items.push(['itemDetails','info',()=>showItemDetails(entry)]);
  if (entry.path) {
    items.push(['rename','edit',()=>changePath(entry,false)],['move','move',()=>changePath(entry,true)]);
    if (entry.kind==='file' || entry.kind==='directory') items.push(['permissions','permissions',()=>changePermissions(entry)]);
    items.push(['delete','trash',()=>deleteEntry(entry)]);
  }
  openMenu(items,anchor,point);
}

function permissionControls(input,label,isDirectory) {
  const panel=document.createElement('div');
  panel.className='permission-controls';
  const presets=document.createElement('div');
  presets.className='permission-presets';
  const advanced=document.createElement('details');
  advanced.className='permission-advanced';
  const summary=document.createElement('summary');
  summary.dataset.i18n='advancedPermissions';
  input.inputMode='numeric';
  input.pattern='0?[0-7]{3}';
  input.required=true;
  input.autofocus=false;
  input.oninvalid=()=>{
    advanced.open=true;
    input.setCustomValidity(t('invalidPermissions'));
  };
  const levels=[
    ['editAccess',isDirectory?7:6],
    ['readAccess',isDirectory?5:4],
    ['noAccess',0],
  ];
  const table=document.createElement('table');
  table.className='permission-grid';
  const header=table.createTHead().insertRow();
  header.append(document.createElement('td'));
  const operations=['readPermission','writePermission','executePermission'];
  for (const operation of operations) {
    const cell=document.createElement('th');
    cell.scope='col';
    cell.dataset.i18n=operation;
    header.append(cell);
  }
  const body=table.createTBody(), boxes=[], selectors=[];
  let mode=parseInt(input.value,8);
  for (const [index,group] of ['ownerPermission','groupPermission','otherPermission'].entries()) {
    const shift=6-index*3;
    const presetLabel=document.createElement('label');
    const caption=document.createElement('span');
    caption.dataset.i18n=group;
    const select=document.createElement('select');
    if (index===0) select.autofocus=true;
    for (const [key,value] of [...levels,['customAccess','custom']]) {
      const option=document.createElement('option');
      option.value=String(value);
      option.dataset.i18n=key;
      option.disabled=value==='custom';
      select.append(option);
    }
    select.onchange=()=>setMode((mode & ~(7 << shift)) | (Number(select.value) << shift));
    presetLabel.append(caption,select);
    presets.append(presetLabel);
    selectors.push({select,shift});

    const row=body.insertRow(), heading=document.createElement('th');
    heading.scope='row';
    heading.dataset.i18n=group;
    row.append(heading);
    for (const [column,operation] of operations.entries()) {
      const checkbox=document.createElement('input');
      checkbox.type='checkbox';
      row.insertCell().append(checkbox);
      boxes.push({checkbox,group,operation,bit:1 << (8-index*3-column)});
    }
  }
  const sync=()=>{
    for (const {checkbox,bit} of boxes) checkbox.checked=Boolean(mode & bit);
    for (const {select,shift} of selectors) {
      const access=(mode >> shift) & 7;
      select.value=levels.some(([,value])=>value===access)?String(access):'custom';
    }
  };
  function setMode(next) {
    mode=next;
    input.value=mode.toString(8).padStart(3,'0');
    input.setCustomValidity('');
    sync();
  }
  input.addEventListener('input',()=>{
    if (/^0?[0-7]{3}$/.test(input.value)) mode=parseInt(input.value,8);
    sync();
  });
  table.onchange=()=>setMode(boxes.reduce((bits,{checkbox,bit})=>checkbox.checked?bits|bit:bits,0));
  if (isDirectory) {
    const hint=document.createElement('p');
    hint.className='permission-hint';
    hint.dataset.i18n='directoryAccessHint';
    panel.append(hint);
  }
  advanced.append(summary,table,label);
  panel.append(presets,advanced);
  sync();
  return {panel,translateLabels(){
    for (const {checkbox,group,operation} of boxes) checkbox.setAttribute('aria-label',t(group)+' · '+t(operation));
  }};
}

function promptDialog(opts) {
  return new Promise(resolve=>{
    const form=document.createElement('form');
    form.className='dialog-form';
    const heading=document.createElement('h2');
    form.append(heading);
    const message=document.createElement('p');
    if (opts.message) form.append(message);
    let input, modeControls, hiddenToggle;
    if (opts.field) {
      const label=document.createElement('label');
      label.innerHTML='<span data-i18n="'+opts.field+'"></span>';
      input=document.createElement('input');
      input.value=opts.value??'';
      input.maxLength=opts.field==='name'?255:4096;
      input.autocomplete='off';
      input.spellcheck=false;
      input.autofocus=true;
      label.append(input);
      if (opts.field==='moveTarget') {
        const hint=document.createElement('small');
        hint.dataset.i18n='rootHint';
        label.append(hint);
        const list=document.createElement('datalist');
        list.id='directory-options';
        list.append(...folders().map(path=>{
          const option=document.createElement('option');
          option.value=path;
          return option;
        }));
        input.setAttribute('list',list.id);
        label.append(list);
      }
      if (opts.field==='mode') {
        modeControls=permissionControls(input,label,opts.isDirectory);
        form.append(modeControls.panel);
        if (opts.isDirectory) {
          const toggleLabel=document.createElement('label');
          toggleLabel.className='folder-visibility';
          hiddenToggle=document.createElement('input');
          hiddenToggle.type='checkbox';
          hiddenToggle.checked=opts.folderHidden;
          const caption=document.createElement('span');
          caption.dataset.i18n='hideFolder';
          toggleLabel.append(hiddenToggle,caption);
          form.append(toggleLabel);
        }
      } else form.append(label);
    }
    const controls=document.createElement('div');
    controls.className='dialog-actions';
    const cancel=document.createElement('button'), submit=document.createElement('button');
    cancel.type='button';
    cancel.className='button';
    cancel.dataset.i18n='cancel';
    if (!input) cancel.autofocus=true;
    cancel.onclick=()=>dialog.close();
    submit.type='submit';
    submit.className=opts.danger?'danger':'primary';
    submit.dataset.i18n=opts.submit || 'confirm';
    controls.append(cancel);
    if (opts.alt) {
      controls.append(makeButton(opts.alt,'close',()=>{
        value=opts.alt;
        dialog.close();
      },'button',true));
    }
    controls.append(submit);
    form.append(controls);
    let value=null;
    form.onsubmit=(e)=>{
      e.preventDefault();
      if (opts.field==='name' && !cleanName(input.value)) {
        input.setCustomValidity(t('invalidPath'));
        input.reportValidity();
        return;
      }
      value=hiddenToggle?{permissions:input.value,hidden:hiddenToggle.checked}:input?input.value:true;
      dialog.close();
    };
    if (input) input.oninput=()=>input.setCustomValidity('');
    dialog.replaceChildren(form);
    dialog.onclose=()=>{
      if (dialog.parentElement===editorPane) document.body.append(dialog);
      modal=null;
      resolve(value);
    };
    modal=()=>{
      heading.textContent=t(opts.title,opts.values);
      if (opts.message) message.textContent=t(opts.message,opts.values);
      translate(dialog);
      modeControls?.translateLabels();
    };
    modal();
    if (isPreviewFullscreen()) editorPane.append(dialog);
    dialog.showModal();
    if (!modeControls) input?.select();
  });
}

async function changePermissions(entry) {
  try {
    const latest=await json(query('stat',entry.path));
    if (latest.kind!==entry.kind) throw {error:'changed'};
    const isDirectory=latest.kind==='directory';
    const wasHidden=hiddenFolders.has(latest.path);
    const result=await promptDialog({
      title:'permissions',message:'permissionsTarget',values:{name:absolute(latest.path)},
      field:'mode',value:latest.permissions,submit:'save',isDirectory,folderHidden:wasHidden,
    });
    if (result===null) return;
    const permissions=isDirectory?result.permissions:result;
    const chmod=permissions.slice(-3)!==latest.permissions;
    const visibilityChanged=isDirectory && result.hidden!==wasHidden;
    if (!chmod && !visibilityChanged) return;
    if (chmod) await action({action:'chmod',path:latest.path,version:latest.version,permissions});
    if (visibilityChanged) {
      if (result.hidden) hiddenFolders.add(latest.path);
      else hiddenFolders.delete(latest.path);
      saveVisibility();
      revealHidden(showHidden);
    }
    if (chmod) await refresh();
    toast('operationDone');
  } catch(error) {tell(error);}
}

async function createFolder(path) {
  const name=await promptDialog({title:'newFolder',field:'name'});
  if (name===null) return;
  try {
    await action({action:'mkdir',path:join(path,name)});
    expanded.add(path);
    await refresh();
    toast('operationDone');
  } catch(e) {tell(e);}
}
async function extractZip(entry) {
  const folder=await promptDialog({
    title:'extractZip',message:'extractHint',field:'name',value:entry.name.slice(0,-4),submit:'extractZip',
  });
  if (folder===null) return;
  try {
    toast('zipExtracting',{name:entry.name});
    await action({action:'extractZip',path:entry.path,destination:folder,version:entry.version});
    expanded.add(parent(entry.path));
    await refresh();
    toast('zipExtracted',{name:folder});
  } catch (error) {tell(error);}
}
async function changePath(entry,moving) {
  const original=entry.path;
  const decision=current && containsPath(original,current.path)?await reviewEdits():'clean';
  if (!decision) return;
  if (decision==='saved') {
    try {entry=await json(query('stat',original));} catch (error) {tell(error);return;}
  }
  const value=await promptDialog({title:moving?'move':'rename',field:moving?'moveTarget':'name',value:moving?parent(original):entry.name});
  if (value===null) return;
  const destination=moving?join(value,entry.name):join(parent(original),value);
  if (destination===original) return;
  const body={action:'move',path:original,destination,version:entry.version};
  try {
    try {await action(body);} catch(error) {
      if (!moving || error.error!=='conflict' || !error.replaceable || entry.kind!=='file') throw error;
      if (!await promptDialog({title:'replacePrompt',values:{name:entry.name},submit:'replace',danger:true})) return;
      await action({...body,overwrite:error.version});
    }
    moveHiddenPaths(original,destination);
    const related=(path)=>path===original||path.startsWith(original+'/');
    const updated=(path)=>destination+path.slice(original.length);
    for (const path of [...expanded]) {
      if (related(path)) {expanded.delete(path);expanded.add(updated(path));}
    }
    expanded.add(parent(destination));
    if (related(selected)) selected=updated(selected);
    if (current && related(current.path)) {
      await showFile({...current,path:updated(current.path),name:current.path===original?(moving?entry.name:value):current.name},{focus:false,closeDrawer:false,changeTarget:false,discardApproved:true});
    }
    await refresh();
    toast('operationDone');
  } catch(e) {tell(e);}
}
async function deleteEntry(entry) {
  const decision=current && containsPath(entry.path,current.path)?await reviewEdits():'clean';
  if (!decision) return;
  if (decision==='saved') {
    try {entry=await json(query('stat',entry.path));} catch (error) {tell(error);return;}
  }
  if (!await promptDialog({title:'deleteQuestion',values:{name:entry.name},message:entry.kind==='directory'?'deleteDirectory':'',submit:'delete',danger:true})) return;
  try {
    const item=await action({action:'delete',path:entry.path,version:entry.version});
    const related=(path)=>path===entry.path||path.startsWith(entry.path+'/');
    if (current && related(current.path)) closePreview(true);
    if (related(selected)) selected=parent(entry.path);
    await refresh();
    toast('trashMoved',{name:entry.name},false,{id:item.id,label:'undo',icon:'restore',run:()=>changeTrash(item,'restore')});
  } catch(e) {tell(e);}
}

async function deleteMarked() {
  if (bulkBusy || !marked.size) return;
  bulkBusy=true;
  updateBulkActions();
  try {
    const entries=[...marked.values()].filter(entry=>{
      for (let path=parent(entry.path);path;path=parent(path)) if (marked.has(path)) return false;
      return true;
    });
    const decision=current && entries.some(entry=>containsPath(entry.path,current.path))?await reviewEdits():'clean';
    if (!decision) return;
    if (!await promptDialog({title:'deleteManyQuestion',values:{count:entries.length},message:entries.some(entry=>entry.kind==='directory')?'deleteDirectory':'',submit:'delete',danger:true})) return;
    let removed=0, firstError;
    for (let entry of entries) {
      try {
        if (decision==='saved' && current && containsPath(entry.path,current.path)) entry=await json(query('stat',entry.path));
        await action({action:'delete',path:entry.path,version:entry.version});
        removed++;
        for (const path of marked.keys()) if (containsPath(entry.path,path)) marked.delete(path);
        if (current && containsPath(entry.path,current.path)) closePreview(true);
        if (containsPath(entry.path,selected)) selected=parent(entry.path);
        if (gridPath!==null && containsPath(entry.path,gridPath)) gridPath=parent(entry.path);
      } catch(error) {firstError??=error;}
    }
    await refresh();
    const failed=entries.length-removed;
    toast('bulkResult',{removed,failed},failed>0);
    if (firstError) tell(firstError);
  } finally {
    bulkBusy=false;
    renderTree();
  }
}

function rootSettings(signal) {
  const section=document.createElement('section');
  section.className='settings-section';
  const label=document.createElement('label');
  label.htmlFor='settings-root';
  label.dataset.i18n='rootLabel';
  const input=document.createElement('input');
  input.id='settings-root';
  input.type='text';
  input.disabled=true;
  input.autocomplete='off';
  input.spellcheck=false;
  const info=document.createElement('p');
  const status=document.createElement('output');
  status.setAttribute('aria-live','polite');
  let loaded=false, writable=false, busy=false;
  function controls() {
    input.disabled=!writable || busy;
    check.disabled=save.disabled=input.disabled || input.value==='';
    restart.disabled=!loaded || busy;
  }
  async function run(task) {
    busy=true;
    controls();
    try {await task();}
    catch(error) {if (!signal.aborted) feedback(errorOf(error),true);}
    finally {busy=false;controls();}
  }
  function feedback(message, failed=false, success=false) {
    status.textContent=message;
    status.classList.toggle('root-check-error',failed);
    status.classList.toggle('root-check-success',success);
  }
  const check=makeButton('rootCheck','search',()=>run(async()=>{
    feedback(t('rootChecking'));
    await json('/api/settings/root/check',{method:'POST',body:{root:input.value},signal});
    feedback(t('rootVerified'),false,true);
  }),'button',true);
  const save=makeButton('save','check',()=>run(async()=>{
    const data=await json('/api/settings/root',{method:'PUT',body:{root:input.value}});
    input.value=data.root;
    feedback(t('rootSaved'));
  }),'button',true);
  const restart=makeButton('restartService','refresh',async()=>{
    if (!await reviewEdits() || !window.confirm(t('restartConfirm'))) return;
    await run(async()=>{
      feedback(t('updateRestarting'));
      await json('/api/settings/restart',{method:'POST'});
      await waitForRestart(status);
    });
  },'button',true);
  const field=document.createElement('div');
  field.className='settings-root-field';
  field.append(input,check);
  const actions=document.createElement('div');
  actions.className='cache-actions';
  actions.append(save,restart);
  input.oninput=()=>{feedback('');controls();};
  input.onkeydown=event=>{if (event.key==='Enter' && !save.disabled) save.click();};
  section.append(label,field,status,info,actions);
  controls();
  json('/api/settings/root',{signal}).then(data=>{
    input.value=data.root;
    loaded=true;
    writable=data.writable;
    controls();
    info.textContent=t(data.writable?'rootActive':'rootOverride',{path:data.active});
  }).catch(error=>{if (!signal.aborted) feedback(errorOf(error),true);});
  return section;
}

let updateReminderTimer, lastUpdateNotice;
function stopUpdateReminder() {clearTimeout(updateReminderTimer);}
function startUpdateReminder() {
  stopUpdateReminder();
  const session=generation;
  async function poll() {
    try {
      const data=await json('/api/updates');
      if (!signedIn || session!==generation) return;
      paintUpdateNotice(data);
      if (data.mode==='remind' && data.available && data.latest!==lastUpdateNotice) {
        lastUpdateNotice=data.latest;
        toast('updateAvailable',{version:data.latest});
      }
    } catch {}
    if (signedIn && session===generation) updateReminderTimer=setTimeout(poll,30000);
  }
  poll();
}
function paintUpdateNotice(state) {
  $('settings').classList.toggle('update-available',state.mode!=='manual' && state.available);
}
function updateSettings(signal) {
  const body=document.createElement('section');
  body.className='settings-body';
  const feedback=document.createElement('output'), hint=document.createElement('p');
  const details=document.createElement('dl');
  details.className='cache-stats';
  const fields={};
  for (const [key,label] of [['current','updateVersionCurrent'],['latest','updateVersionLatest'],['platform','installationPlatform'],['date','installationDate']]) {
    const term=document.createElement('dt'), value=document.createElement('dd');
    term.dataset.i18n=label;
    value.textContent='—';
    fields[key]={term,value};
    details.append(term,value);
  }
  hint.dataset.i18n='updateHelp';
  feedback.setAttribute('aria-live','polite');
  const modes=document.createElement('fieldset');
  modes.className='update-modes';
  const legend=document.createElement('legend');
  legend.dataset.i18n='updateMode';
  modes.append(legend);
  const versionField=document.createElement('div');
  versionField.className='update-version-field';
  const label=document.createElement('label'), choices=document.createElement('select');
  label.htmlFor=choices.id='update-target-version';
  label.dataset.i18n='updateChooseVersion';
  versionField.append(label,choices);
  const versionsHint=document.createElement('p');
  versionsHint.dataset.i18n='updateVersionHint';
  let state, nextPage=1, loadingVersions=false, installing=false;
  const releases=new Set();
  const radios={};
  function paint(data) {
    state=data;
    fields.current.value.textContent=data.current;
    fields.latest.value.textContent=data.latest || '—';
    fields.platform.value.textContent=data.platform || '—';
    const date=data.installedAt && new Date(data.installedAt);
    const dateLabel=data.installationExact?'installationDate':'installationObservedDate';
    fields.date.term.dataset.i18n=dateLabel;
    fields.date.term.textContent=t(dateLabel);
    fields.date.value.textContent=date && Number.isFinite(date.valueOf())
      ? new Intl.DateTimeFormat(language==='zh'?'zh-CN':'en',{dateStyle:'medium'}).format(date)
      : '—';
    modes.disabled=false;
    radios[data.mode].checked=true;
    radios.auto.disabled=!data.supported;
    paintVersions();
    paintUpdateNotice(data);
  }
  function paintVersions() {
    const selected=choices.value;
    const placeholder=document.createElement('option');
    placeholder.value='';
    placeholder.textContent=t('updateChooseVersion');
    const available=new Set(releases);
    choices.replaceChildren(placeholder);
    for (const version of [...available].sort((a,b)=>b.localeCompare(a,undefined,{numeric:true}))) {
      const option=document.createElement('option');
      option.value=version;
      option.disabled=version===state?.current;
      option.textContent=option.disabled?t('updateInstalledOption',{version}):version;
      choices.append(option);
    }
    if (available.has(selected) && selected!==state?.current) choices.value=selected;
    else if (state?.available && available.has(state.latest)) choices.value=state.latest;
    choices.disabled=installing || !state?.supported || ![...choices.options].some(option=>option.value && !option.disabled);
    install.disabled=choices.disabled || !choices.value;
    moreVersions.hidden=!nextPage;
    moreVersions.disabled=loadingVersions || installing;
  }
  async function loadVersions() {
    if (!nextPage || loadingVersions) return;
    loadingVersions=true;
    paintVersions();
    try {
      const data=await json('/api/updates/versions?page='+nextPage,{signal:signal});
      for (const tag of data.versions) releases.add(tag);
      nextPage=data.nextPage || 0;
    } catch(error) {
      if (!signal.aborted) feedback.textContent=errorOf(error);
    } finally {loadingVersions=false;paintVersions();}
  }
  async function changeMode(mode) {
    modes.disabled=true;
    try {paint(await json('/api/updates',{method:'PUT',body:{mode}}));}
    catch(error) {feedback.textContent=errorOf(error);paint(state);}
  }
  for (const [mode,key] of [['manual','updateManual'],['remind','updateRemind'],['auto','updateAuto']]) {
    const row=document.createElement('label'), choice=document.createElement('input'), caption=document.createElement('span');
    choice.type='radio';choice.name='update-mode';choice.value=mode;
    choice.onchange=()=>changeMode(mode);
    caption.dataset.i18n=key;
    row.append(choice,caption);
    modes.append(row);
    radios[mode]=choice;
  }
  modes.disabled=true;
  const check=makeButton('checkUpdates','refresh',async()=>{
    check.disabled=true;
    feedback.textContent=t('checkingUpdates');
    try {
      const data=await json('/api/updates/check',{method:'POST',signal:signal});
      paint(data);
      feedback.textContent=t(data.available?'updateAvailable':data.supported?'updateCurrent':'updateUnsupported',{version:data.latest});
    } catch(error) {if (!signal.aborted) feedback.textContent=errorOf(error);}
    finally {check.disabled=false;}
  },'button',true);
  const install=makeButton('updateInstall','download',async()=>{
    const version=choices.value;
    if (!version || installing) return;
    const older=version.localeCompare(state.current,undefined,{numeric:true})<0;
    if (!await reviewEdits() || !window.confirm(t(older?'updateRollbackConfirm':'updateRestart',{version}))) return;
    installing=true;
    paintVersions();
    feedback.textContent=t('updating');
    try {
      await json('/api/updates/install',{method:'POST',body:{version}});
      feedback.textContent=t('updateRestarting');
      await waitForRestart(feedback,version);
    } catch(error) {feedback.textContent=errorOf(error);}
    finally {installing=false;paintVersions();}
  },'button',true);
  const moreVersions=makeButton('updateMoreVersions','down',loadVersions,'button',true);
  choices.onchange=()=>{install.disabled=!choices.value || installing;};
  const actions=document.createElement('div');
  actions.className='cache-actions';
  actions.append(check,install,moreVersions);
  body.append(details,versionField,actions,feedback,versionsHint,modes,hint);
  paintVersions();
  loadVersions();
  json('/api/updates',{signal:signal}).then(paint).catch(error=>{
    if (!signal.aborted) feedback.textContent=errorOf(error);
  });
  return body;
}
async function waitForRestart(feedback,version) {
  for (let attempt=0;attempt<20;attempt++) {
    await new Promise(resolve=>setTimeout(resolve,1000));
    // A service restart clears in-memory sessions, so reload when the new server responds.
    try {
      const reply=await fetch('/api/updates');
      if (reply.status===401 || (version && reply.ok && (await reply.json()).current===version)) {
        location.reload();
        return;
      }
    } catch {}
  }
  feedback.textContent=t(version?'updateReconnect':'restartReconnect');
}

function aboutSettings() {
  const section=document.createElement('section');
  section.className='settings-about';
  const title=document.createElement('h3');
  title.dataset.i18n='settingsAbout';
  const details=document.createElement('dl');
  for (const [label,name,url] of [
    ['projectAddress','github.com/L-use/Linux-file-manager','https://github.com/L-use/Linux-file-manager'],
    ['projectAuthor','L-use','https://github.com/L-use']
  ]) {
    const term=document.createElement('dt'), value=document.createElement('dd'), link=document.createElement('a');
    term.dataset.i18n=label;
    link.textContent=name;
    link.href=url;
    link.target='_blank';
    link.rel='noopener noreferrer';
    value.append(link);
    details.append(term,value);
  }
  section.append(title,details);
  return section;
}

let pendingCacheWrite;
function cacheSettings(signal) {
  const body=document.createElement('section');
  body.className='settings-body';
  const description=document.createElement('p');
  description.dataset.i18n='cacheHelp';
  const stats=document.createElement('dl');
  stats.className='cache-stats';
  const usage={};
  for (const [key,label] of [['total','cacheTotal'],['thumbnails','cacheThumbnails'],['videos','cacheVideos']]) {
    const term=document.createElement('dt'), value=document.createElement('dd');
    term.dataset.i18n=label;
    value.textContent='—';
    usage[key]=value;
    stats.append(term,value);
  }
  const slider=document.createElement('input');
  slider.type='range';slider.min='64';slider.max='4096';slider.step='64';slider.value='1024';
  slider.id='preview-cache-limit';
  const limitLabel=document.createElement('label');
  limitLabel.htmlFor=slider.id;
  limitLabel.className='cache-limit-label';
  const label=document.createElement('span');
  label.dataset.i18n='cacheLimit';
  const amount=document.createElement('output');
  limitLabel.append(label,amount);
  const status=document.createElement('output');
  status.setAttribute('aria-live','polite');
  let snapshot, busy=false, revision=0, timer;
  function paint() {
    amount.textContent=snapshot?slider.value+' MiB':'—';
    slider.setAttribute('aria-valuetext',amount.textContent);
    slider.disabled=busy || !snapshot;
    clear.disabled=busy || !snapshot;
    save.disabled=busy || !snapshot || Number(slider.value)===snapshot.limitMiB;
  }
  function apply(data) {
    const editing=snapshot && Number(slider.value)!==snapshot.limitMiB;
    snapshot=data;
    const {thumbnails,videos}=data;
    usage.total.textContent=size(thumbnails.bytes+videos.bytes)+' / '+size(data.limitMiB*1024*1024);
    for (const key of ['thumbnails','videos']) usage[key].textContent=size(data[key].bytes)+' · '+data[key].count;
    if (!editing) slider.value=String(data.limitMiB);
    paint();
  }
  async function change(route,options,message) {
    revision++;
    busy=true;
    paint();
    status.textContent='';
    try {
      const request=json(route,options);
      pendingCacheWrite=request;
      const data=await request;
      if (route==='/api/cache/clear') {
        gridPreview?.abort();
        gridThumbnails.clear();
      }
      slider.value=String(snapshot.limitMiB);
      apply(data);
      status.textContent=t(message);
    } catch(error) {
      if (!signal.aborted) status.textContent=errorOf(error);
    } finally {busy=false;paint();}
  }
  const save=makeButton('cacheSave','check',()=>change('/api/cache',{method:'PUT',body:{limitMiB:Number(slider.value)}},'cacheSaved'),'button',true);
  const clear=makeButton('clearCache','trash',()=>change('/api/cache/clear',{method:'POST'},'cacheCleared'),'button',true);
  const actions=document.createElement('div');
  actions.className='cache-actions';
  actions.append(save,clear);
  slider.oninput=paint;
  slider.onchange=()=>{if (!save.disabled) save.click();};
  body.append(description,stats,limitLabel,slider,actions,status);
  signal.addEventListener('abort',()=>clearTimeout(timer),{once:true});
  paint();
  async function refreshStats() {
    if (!busy && !body.hidden) {
      const requested=revision;
      try {
        await pendingCacheWrite?.catch(()=>{});
        const data=await json('/api/cache',{signal:signal});
        if (!busy && requested===revision && !signal.aborted) apply(data);
      } catch(error) {
        if (!snapshot && !signal.aborted) status.textContent=errorOf(error);
      }
    }
    if (!signal.aborted) timer=setTimeout(refreshStats,1500);
  }
  refreshStats();
  return body;
}

function appearanceSettings() {
  const section=document.createElement('section');
  section.className='settings-body settings-appearance';
  const themeLabel=document.createElement('label');
  themeLabel.htmlFor='settings-theme';
  themeLabel.dataset.i18n='settingsTheme';
  const theme=makeButton('settingsTheme','sun',()=>{
    const night=document.documentElement.dataset.theme!=='night';
    applyTheme(night);
    try {localStorage.setItem('fm-theme',night?'night':'day');} catch {}
    paintTheme();
  },'button theme',true);
  theme.id='settings-theme';
  function paintTheme() {
    const night=document.documentElement.dataset.theme==='night';
    theme.setAttribute('aria-pressed',String(night));
    theme.querySelector('svg').outerHTML=icon(night?'moon':'sun');
    theme.querySelector('span').dataset.i18n=night?'nightMode':'dayMode';
    translate(section);
  }
  const languageLabel=document.createElement('label');
  languageLabel.htmlFor='settings-language';
  languageLabel.dataset.i18n='settingsLanguage';
  const choice=document.createElement('select');
  choice.id='settings-language';
  for (const [value,name] of [['zh','中文'],['en','English']]) {
    const option=document.createElement('option');
    option.value=value;
    option.textContent=name;
    choice.append(option);
  }
  choice.value=language;
  choice.onchange=()=>switchLanguage(choice.value);
  section.append(themeLabel,theme,languageLabel,choice);
  paintTheme();
  return section;
}
function accountSettings() {
  const section=document.createElement('section');
  section.className='settings-body';
  const name=document.createElement('strong'), feedback=document.createElement('output');
  name.textContent=sessionUser;
  feedback.setAttribute('aria-live','polite');
  const logout=makeButton('logout','logout',async()=>{
    if (!await reviewEdits()) return;
    logout.disabled=true;
    try {await json('/api/logout',{method:'POST'});showLogin();}
    catch(error) {feedback.textContent=errorOf(error);}
    finally {logout.disabled=false;}
  },'button',true);
  section.append(name,logout,feedback);
  return section;
}
function openSettings() {
  if ($('tools-dialog').open) return;
  const controller=new AbortController();
  const layout=document.createElement('div'), navigation=document.createElement('div'), content=document.createElement('div');
  layout.className='settings-layout';
  navigation.className='settings-nav';
  navigation.setAttribute('role','tablist');
  navigation.dataset.label='settings';
  content.className='settings-content';
  const categories=[['settingsAppearance','sun',appearanceSettings]];
  if (signedIn) categories.push(
    ['settingsServer','server',()=>rootSettings(controller.signal)],
    ['settingsCache','archive',()=>cacheSettings(controller.signal)],
    ['account','user',accountSettings],
    ['settingsUpdates','refresh',()=>updateSettings(controller.signal)]
  );
  categories.push(['settingsAbout','info',aboutSettings]);
  const tabs=[], panels=[];
  function select(index,focus=false) {
    const [label,,create]=categories[index];
    if (!panels[index]) {
      const panel=create();
      panel.classList.add('settings-tab-panel');
      panel.setAttribute('role','tabpanel');
      panel.setAttribute('aria-labelledby','settings-tab-'+label);
      panel.id='settings-panel-'+label;
      panels[index]=panel;
      content.append(panel);
      translate(panel);
    }
    tabs.forEach((tab,i)=>{
      tab.setAttribute('aria-selected',String(i===index));
      tab.tabIndex=i===index?0:-1;
      if (panels[i]) panels[i].hidden=i!==index;
    });
    content.scrollTop=0;
    if (focus) tabs[index].focus();
  }
  categories.forEach(([label,glyph],i)=>{
    const tab=makeButton(label,glyph,()=>select(i),'button',true);
    tab.setAttribute('role','tab');
    tab.id='settings-tab-'+label;
    tab.setAttribute('aria-controls','settings-panel-'+label);
    tabs.push(tab);
    navigation.append(tab);
  });
  navigation.onkeydown=event=>{
    const current=tabs.indexOf(event.target);
    if (current<0) return;
    let next;
    if (event.key==='Home') next=0;
    else if (event.key==='End') next=tabs.length-1;
    else if (['ArrowLeft','ArrowUp'].includes(event.key)) next=(current+tabs.length-1)%tabs.length;
    else if (['ArrowRight','ArrowDown'].includes(event.key)) next=(current+1)%tabs.length;
    else return;
    event.preventDefault();
    select(next,true);
  };
  layout.append(navigation,content);
  select(0);
  openToolPanel('settings',layout,()=>controller.abort());
}

function openToolPanel(title,content,onClose=()=>{}) {
  const panel=$('tools-dialog');
  if (panel.open) return null;
  const heading=document.createElement('h2');
  heading.dataset.i18n=title;
  heading.id='tools-title';
  const header=document.createElement('header');
  header.className='tools-dialog-header';
  header.append(heading,makeButton('close','close',()=>panel.close()));
  panel.setAttribute('aria-labelledby',heading.id);
  panel.replaceChildren(header,content);
  panel.onclose=()=>{
    if (panel.parentElement===editorPane) document.body.append(panel);
    onClose();
  };
  translate(panel);
  if (isPreviewFullscreen()) editorPane.append(panel);
  panel.showModal();
  return panel;
}

function detailsRow(list,key,value,translateValue=false) {
  const label=document.createElement('dt'), detail=document.createElement('dd');
  label.dataset.i18n=key;
  if (translateValue) detail.dataset.i18n=value;
  else detail.textContent=value;
  list.append(label,detail);
}
async function showItemDetails(entry) {
  const body=document.createElement('div');
  listingState(body,'loading');
  const controller=new AbortController();
  const panel=openToolPanel('itemDetails',body,()=>controller.abort());
  if (!panel) return;
  const active=()=>panel.open && !controller.signal.aborted;
  try {
    const data=await json(query('details',entry.path),{signal:controller.signal});
    if (!active()) return;
    const numeric=new Intl.NumberFormat(language==='zh'?'zh-CN':'en');
    const date=new Intl.DateTimeFormat(language==='zh'?'zh-CN':'en',{
      dateStyle:'medium',timeStyle:'medium',
    });
    const bytes=value=>value<1024?size(value):size(value)+' ('+numeric.format(value)+' B)';
    const account=(name,id)=>name?name+' ('+id+')':String(id);
    const list=document.createElement('dl');
    list.className='file-details';
    const type={directory:'detailFolder',file:'detailFile',symlink:'detailSymlink',special:'detailSpecial'};
    const subtype={fifo:'detailFIFO',socket:'detailSocket',charDevice:'detailCharDevice',blockDevice:'detailBlockDevice'};
    detailsRow(list,'name',data.name);
    detailsRow(list,'path',absolute(data.path));
    detailsRow(list,'detailType',subtype[data.subtype] || type[data.kind],true);
    detailsRow(list,data.kind==='directory'?'detailFolderEntrySize':'detailSize',bytes(data.size));
    detailsRow(list,'detailAllocated',bytes(data.allocated));
    if (data.created!=null) detailsRow(list,'detailCreated',date.format(data.created));
    detailsRow(list,'detailModified',date.format(data.modified));
    detailsRow(list,'detailAccessed',date.format(data.accessed));
    detailsRow(list,'detailChanged',date.format(data.changed));
    detailsRow(list,'detailOwner',account(data.owner,data.uid));
    detailsRow(list,'detailGroup',account(data.group,data.gid));
    detailsRow(list,'detailMode',data.symbolic+' ('+data.mode+')');
    detailsRow(list,'detailInode',data.inode);
    detailsRow(list,'detailLinks',numeric.format(data.links));
    body.replaceChildren(list);
    if (data.kind==='directory') {
      const summary=document.createElement('section');
      summary.className='folder-details';
      const result=document.createElement('div');
      const calculate=makeButton('detailCalculate','folder',async()=>{
        calculate.disabled=true;
        listingState(result,'loading');
        try {
          const total=await json(query('folder-contents',entry.path),{signal:controller.signal});
          if (!active()) return;
          const totals=document.createElement('dl');
          totals.className='file-details';
          detailsRow(totals,'detailContentsSize',bytes(total.bytes));
          detailsRow(totals,'detailFiles',numeric.format(total.files));
          detailsRow(totals,'detailSubfolders',numeric.format(total.folders));
          result.replaceChildren(totals);
          if (total.partial) {
            const note=document.createElement('p');
            note.className='listing-state';
            note.dataset.i18n='detailPartial';
            result.append(note);
          }
          translate(result);
          calculate.hidden=true;
        } catch (error) {
          if (active()) listingState(result,error.error || 'readError');
        } finally {calculate.disabled=false;}
      },'button',true);
      summary.append(calculate,result);
      body.append(summary);
    }
    translate(body);
  } catch (error) {
    if (active()) listingState(body,error.error || 'readError');
  }
}

function listingState(list,key,values={}) {
  const message=document.createElement('p');
  message.className='listing-state';
  message.textContent=t(key,values);
  list.replaceChildren(message);
}

function listingRow(entry,secondary) {
  const row=document.createElement('div');
  row.className='listing-row';
  const type=fileType(entry);
  const mark=document.createElement('span');
  mark.className='type-'+type.style;
  mark.innerHTML=icon(type.icon);
  const text=document.createElement('div');
  text.className='listing-text';
  const name=document.createElement('strong'), detail=document.createElement('small');
  name.textContent=name.title=entry.name;
  detail.textContent=detail.title=secondary;
  text.append(name,detail);
  return {row,mark,text};
}

async function changeTrash(item,operation,onError=tell) {
  if (operation==='purge' && !window.confirm(t('purgeConfirm',{name:item.name}))) return false;
  try {
    await json('/api/trash/'+operation,{method:'POST',body:{id:item.id}});
    $('toasts').querySelector(`[data-trash-undo="${CSS.escape(item.id)}"]`)?.remove();
    toast(operation==='restore'?'trashRestored':'trashPurged',{name:item.name});
    if (operation==='restore') await refresh();
    return true;
  } catch(error) {onError(error);return false;}
}

function openTrashPanel() {
  const body=document.createElement('section');
  const list=document.createElement('div');
  list.className='tool-list';
  const toolbar=document.createElement('div');
  toolbar.className='trash-toolbar';
  const notice=document.createElement('p');
  notice.className='listing-state';
  notice.setAttribute('role','status');
  notice.hidden=true;
  const clear=makeButton('trashClear','trash',async()=>{
    if (!await promptDialog({title:'trashClearConfirm',message:'trashClearWarning',submit:'trashClear',danger:true}) || !panel.open) return;
    clear.disabled=true;
    list.inert=true;
    notice.hidden=false;
    notice.textContent=t('trashClearing');
    try {
      const result=await json('/api/trash/clear',{method:'POST',body:{}});
      if (!panel.open) return;
      notice.textContent=t(result.failed?'trashClearPartial':'trashCleared',{removed:result.removed,failed:result.failed});
      await update(true);
    } catch(error) {
      if (panel.open) notice.textContent=errorOf(error);
    } finally {clear.disabled=false;list.inert=false;}
  },'button purge-button',true);
  clear.hidden=true;
  toolbar.append(clear);
  body.append(toolbar,notice,list);
  const panel=openToolPanel('trash',body);
  if (!panel) return;
  async function update(reconcileUndo=false) {
    listingState(list,'loading');
    clear.hidden=true;
    try {
      const entries=await json('/api/trash');
      if (!list.isConnected || !panel.open) return;
      if (reconcileUndo) {
        const remaining=new Set(entries.map(item=>item.id));
        for (const button of $('toasts').querySelectorAll('[data-trash-undo]')) {
          if (!remaining.has(button.dataset.trashUndo)) button.remove();
        }
      }
      if (!entries.length) {listingState(list,'trashEmpty');return;}
      clear.hidden=false;
      const fragment=document.createDocumentFragment();
      for (const item of entries) {
        const date=new Date(item.deleted).toLocaleString(language==='zh'?'zh-CN':'en');
        const {row,mark,text}=listingRow(item,absolute(item.path)+' · '+date);
        const notice=document.createElement('small');
        notice.className='listing-error';
        notice.setAttribute('role','alert');
        notice.hidden=true;
        const commands=document.createElement('div');
        commands.className='listing-actions';
        for (const [action,glyph] of [['restore','restore'],['purge','trash']]) {
          const button=makeButton(action,glyph,async()=>{
            for (const control of commands.children) control.disabled=true;
            notice.hidden=true;
            const updated=await changeTrash(item,action,error=>{
              notice.textContent=errorOf(error);
              notice.hidden=false;
            });
            if (updated) await update();
            else for (const control of commands.children) control.disabled=false;
          },'button '+(action==='purge'?'purge-button':''),true);
          commands.append(button);
        }
        text.append(notice);
        row.append(mark,text,commands);
        fragment.append(row);
      }
      list.replaceChildren(fragment);
      translate(list);
    } catch(error) {listingState(list,error?.error || 'network');}
  }
  update();
}

function openSearchPanel() {
  const path=targetDirectory();
  const body=document.createElement('section');
  body.className='tool-body';
  const scope=document.createElement('p');
  scope.className='listing-scope';
  scope.textContent=t('searchScope',{name:absolute(path)});
  const form=document.createElement('form');
  form.className='subtree-search';
  const input=document.createElement('input');
  input.type='search';
  input.value=search.value;
  input.required=true;
  input.autofocus=true;
  input.dataset.label=input.dataset.placeholder='search';
  const submit=makeButton('searchSubmit','search',()=>{},'primary',true);
  submit.type='submit';
  const list=document.createElement('div');
  list.className='tool-list';
  list.setAttribute('aria-live','polite');
  form.append(input,submit);
  body.append(scope,form,list);
  let request;
  const panel=openToolPanel('searchSubtree',body,()=>request?.abort());
  if (!panel) return;
  form.onsubmit=async(e)=>{
    e.preventDefault();
    const textValue=input.value.trim();
    if (!textValue) return;
    request?.abort();
    const controller=new AbortController();
    request=controller;
    listingState(list,'loading');
    try {
      const result=await json(query('search',path,{q:textValue}),{signal:controller.signal});
      if (controller.signal.aborted || !panel.open) return;
      const matches=result.matches.filter(entry=>isVisiblePath(entry.path));
      if (!matches.length) listingState(list,'noMatches');
      else {
        list.replaceChildren(...matches.map(entry=>{
          const {row,mark,text}=listingRow(entry,absolute(entry.path));
          row.classList.add('search-hit');
          row.setAttribute('aria-label',absolute(entry.path));
          const button=document.createElement('button');
          button.type='button';
          button.className='listing-open';
          button.append(mark,text);
          button.onclick=async()=>{
            panel.close();
            if (entry.kind==='directory') await locate(entry.path);
            else {
              await locate(parent(entry.path));
              await showFile(entry);
            }
          };
          row.append(button);
          return row;
        }));
      }
      if (result.limited) {
        const hint=document.createElement('p');
        hint.className='listing-state';
        hint.textContent=t('searchMore');
        list.append(hint);
      }
    } catch(error) {
      if (!controller.signal.aborted && panel.open) listingState(list,error?.error || 'network');
    }
  };
  input.select();
  if (input.value.trim()) form.requestSubmit();
}

let uploadDirectory='';
function pickUpload(path,folder=false) {
  if (uploadState) return;
  uploadDirectory=path;
  $(folder?'folder-files':'files').click();
}
function cancelUpload() {
  if (!uploadState) return;
  uploadState.cancelled=true;
  uploadState.xhr?.abort();
}
function pickedEntries(files,folder) {
  if (!folder) return files.map(file=>({path:file.name,file}));
  const entries=[];
  const directories=new Set();
  for (const file of files) {
    const path=file.webkitRelativePath;
    if (!path || !path.includes('/')) throw {error:'readLocal'};
    const parts=path.split('/');
    let directory='';
    for (const part of parts.slice(0,-1)) {
      directory=join(directory,part);
      if (!directories.has(directory)) {
        directories.add(directory);
        entries.push({path:directory});
      }
    }
    entries.push({path,file});
  }
  return entries;
}
async function droppedEntries(items,state) {
  const entries=[];
  async function visit(entry,base) {
    if (state.cancelled) return;
    const path=join(base,entry.name);
    if (entry.isFile) {
      const file=await new Promise((resolve,reject)=>entry.file(resolve,reject));
      entries.push({path,file});
    } else if (entry.isDirectory) {
      entries.push({path});
      const reader=entry.createReader();
      while (!state.cancelled) {
        const batch=await new Promise((resolve,reject)=>reader.readEntries(resolve,reject));
        if (!batch.length) break;
        for (const child of batch) await visit(child,path);
      }
    }
  }
  for (const {entry,file} of items) {
    if (entry) await visit(entry,'');
    else if (file) entries.push({path:file.name,file});
  }
  return entries;
}
function refreshUploadMeter(state) {
  const panel=$('upload-panel');
  const bar=panel.querySelector('progress');
  const info=panel.querySelector('.transfer-meter');
  if (!bar || !info) return;
  const total=panel.querySelector('.total-upload-meter progress');
  if (total) total.value=bytesPercent(state.completedBytes+state.sent,state.plannedBytes)*100;
  const file=state.active?.file;
  if (!file) {
    bar.hidden=info.hidden=true;
    return;
  }
  bar.hidden=info.hidden=false;
  bar.value=bytesPercent(state.sent,file.size,state.sentBody)*100;
  info.textContent=meterText(state.sent,file.size,state.meter.rate,state.sentBody);
}

function paintUpload() {
  const panel=$('upload-panel');
  panel.hidden=!uploadState && !uploadResult;
  panel.replaceChildren();
  if (panel.hidden) return;
  const header=document.createElement('div');
  header.className='upload-head';
  const title=document.createElement('strong');
  title.dataset.i18n='uploadQueue';
  const dismiss=()=>{
    if (uploadState) cancelUpload();
    else {uploadResult=null;paintUpload();}
  };
  header.append(title,makeButton(uploadState?'cancel':'close','close',dismiss));
  panel.append(header);
  const summary=document.createElement('p');
  summary.className='upload-summary';
  if (uploadState) {
    summary.textContent=t(uploadState.total?'uploading':'scanningUpload',uploadState);
    const name=document.createElement('span');
    name.className='truncate';
    name.textContent=name.title=uploadState.active?.path || '';
    const progress=document.createElement('progress');
    progress.max=100;
    progress.dataset.label='uploadProgress';
    const info=document.createElement('small');
    info.className='transfer-meter';
    panel.append(summary,progress,name,info);
    if (uploadState.plannedBytes>0 && uploadState.showTotal) {
      const group=document.createElement('div');
      group.className='total-upload-meter';
      const label=document.createElement('small');
      label.dataset.i18n='uploadTotalProgress';
      const total=document.createElement('progress');
      total.max=100;
      total.dataset.label='uploadTotalProgress';
      group.append(label,total);
      panel.append(group);
    }
    refreshUploadMeter(uploadState);
  } else {
    const result=uploadResult;
    summary.textContent=t('uploadQueueSummary',{done:result.done,skipped:result.skipped,failed:result.pending.length});
    panel.append(summary);
    if (result.pending.length) {
      const problems=document.createElement('ul');
      problems.className='upload-errors';
      for (const entry of result.pending) {
        const problem=document.createElement('li');
        const name=document.createElement('strong'), reason=document.createElement('small');
        name.textContent=name.title=entry.path;
        reason.textContent=t(entry.error || 'uploadQueued');
        problem.append(name,reason);
        problems.append(problem);
      }
      const retry=makeButton('retryUpload','refresh',()=>uploadEntries(result.path,()=>result.pending,result),'button',true);
      panel.append(problems,retry);
    }
  }
  translate(panel);
}

function uploadFile(state,file,path,overwrite='') {
  return new Promise((resolve,reject)=>{
    const xhr=new XMLHttpRequest();
    state.xhr=xhr;
    state.sent=0;
    state.sentBody=false;
    state.meter=newMeter();
    refreshUploadMeter(state);
    xhr.open('PUT',query('upload',path,{overwrite}));
    xhr.setRequestHeader('X-FM-Request','1');
    xhr.upload.onprogress=(e)=>{
      if (uploadState===state && e.lengthComputable) {
        state.sent=Math.min(e.loaded,file.size);
        sampleRate(state.meter,state.sent);
        refreshUploadMeter(state);
      }
    };
    xhr.upload.onload=()=>{
      if (uploadState!==state) return;
      state.sent=file.size;
      state.sentBody=true;
      sampleRate(state.meter,state.sent);
      refreshUploadMeter(state);
    };
    xhr.onload=()=>{
      if (xhr.status>=200 && xhr.status<300) resolve();
      else {
        if (xhr.status===401) showLogin('unauthenticated');
        try{reject(JSON.parse(xhr.responseText));}catch{reject({error:'io'});}
      }
    };
    xhr.onerror=()=>reject({error:'network'});
    xhr.onabort=()=>reject({error:'interrupted'});
    xhr.send(file);
  });
}

async function uploadEntries(path,readEntries,prior={done:0,skipped:0}) {
  if (uploadState || !signedIn) return;
  const state={
    current:0,total:0,cancelled:false,active:null,sent:0,sentBody:false,meter:newMeter(),
    plannedBytes:prior.plannedBytes,completedBytes:prior.completedBytes || 0,showTotal:prior.showTotal,
  };
  const session=generation;
  let entries=[],done=prior.done,skipped=prior.skipped,skipAll=false;
  const pending=[],blocked=new Set();
  uploadState=state;
  uploadResult=null;
  paintUpload();
  try {
    entries=await readEntries(state);
    if (state.cancelled) return;
    if (!entries.length) {toast('dropEmpty',{},true);return;}
    state.total=entries.length;
    state.plannedBytes ??= entries.reduce((total,entry)=>total+(entry.file?.size || 0),0);
    state.showTotal ??= entries.filter(entry=>entry.file).length>1;
    for (const [index,entry] of entries.entries()) {
      if (state.cancelled) {
        pending.push(...entries.slice(index).map(item=>({...item,error:'uploadQueued'})));
        break;
      }
      const {path:relative,file}=entry;
      state.current++;
      state.active=entry;
      state.sent=0;
      state.sentBody=false;
      state.meter=newMeter();
      paintUpload();
      const destination=join(path,relative);
      try {
        if (blocked.has(parent(relative))) throw {error:'blockedUpload'};
        if (!file) {
          await action({action:'mkdir',path:destination,existOK:true});
        } else {
          try {
            await uploadFile(state,file,destination);
          } catch(error) {
            if (state.cancelled || error.error!=='conflict' || !error.replaceable) throw error;
            const choice=skipAll?'skipAll':await promptDialog({
              title:'replacePrompt',values:{name:relative},submit:'replace',alt:'skipAll',danger:true,
            });
            if (state.cancelled) throw {error:'interrupted'};
            if (choice==='skipAll') skipAll=true;
            if (choice!==true) {skipped++;continue;}
            await uploadFile(state,file,destination,error.version);
          }
        }
        done++;
        state.completedBytes+=file?.size || 0;
      } catch(error) {
        if (!file) blocked.add(relative);
        pending.push({...entry,error:error?.error || 'network'});
      } finally {
        state.xhr=null;
        state.active=null;
      }
    }
  } catch(error) {
    if (!state.cancelled) tell({error:error?.error || 'readLocal'});
  } finally {
    uploadState=null;
    const currentSession=signedIn && generation===session;
    if (currentSession && state.total) uploadResult={path,done,skipped,pending,plannedBytes:state.plannedBytes,completedBytes:state.completedBytes,showTotal:state.showTotal};
    paintUpload();
    if (currentSession && state.total) {
      expanded.add(path);
      await refresh();
    }
  }
}

for (const [id,glyph] of Object.entries({'brand-icon':'server','login-icon':'server','toggle-tree':'explorer','upload':'upload','upload-folder':'uploadFolder','new-folder':'newFolder','tree-sort':'sort','refresh':'refresh','search-icon':'search','search-deep':'searchSubtree','folders-icon':'folder','more':'more'})) $(id).innerHTML=icon(glyph);
$('tree-sort').onclick=openTreeSort;
$('open-trash').insertAdjacentHTML('afterbegin',icon('trash'));
$('open-trash').onclick=openTrashPanel;
$('search-deep').onclick=openSearchPanel;
$('download').insertAdjacentHTML('afterbegin',icon('download'));
$('preview-fullscreen').innerHTML=icon('fullscreen');
$('preview-fullscreen').onclick=fullscreenPreview;
$('show-hidden').onclick=()=>revealHidden(!showHidden);
updateHiddenButton();
document.addEventListener('fullscreenchange',()=>{
  if (!isPreviewFullscreen()) closeMenu(false);
  renderFullscreenButton();
});
function applyTheme(night) {
  const root=document.documentElement;
  if (night) root.dataset.theme='night';
  else delete root.dataset.theme;
  document.querySelector('meta[name="theme-color"]').content=night?'#08110c':'#edf3fc';
}

applyTheme(document.documentElement.dataset.theme==='night');
function switchLanguage(next) {
  setLanguage(next);
  if (signedIn) {renderTree();renderHeader();activePreview?.language();liveStatus.render();}
  $('login-error').textContent=loginError?t(loginError):'';
  modal?.();
  if (popup) {renderMenu();menu.firstElementChild?.focus();}
  for (const notice of $('toasts').children) notice.translateMessage();
  paintUpload();
}
$('login-form').onsubmit=async(e)=>{
  e.preventDefault();
  const form=e.currentTarget, submit=form.querySelector('button[type=submit]');
  submit.disabled=true;
  try {
    await json('/api/login',{method:'POST',body:Object.fromEntries(new FormData(form))});
    form.elements.password.value='';
    await showWorkspace(await json('/api/session'));
  } catch(error) {
    loginError=error.error||'network';
    $('login-error').textContent=errorOf(error);
  } finally {submit.disabled=false;}
};
$('toggle-tree').onclick=()=>setDrawer(!workspace.classList.contains('drawer'));
$('backdrop').onclick=()=>{setDrawer(false);$('toggle-tree').focus();};
$('new-folder').onclick=()=>createFolder(targetDirectory());
$('upload').onclick=()=>pickUpload(targetDirectory());
$('upload-folder').onclick=()=>pickUpload(targetDirectory(),true);
for (const input of [$('files'),$('folder-files')]) {
  input.onchange=()=>{
    const files=[...input.files];
    input.value='';
    if (files.length) uploadEntries(uploadDirectory,()=>pickedEntries(files,input.id==='folder-files'));
  };
}
const explorer=$('explorer');
explorer.ondragover=(e)=>{
  if (!e.dataTransfer.types.includes('Files')) return;
  e.preventDefault();
  e.dataTransfer.dropEffect=uploadState?'none':'copy';
  explorer.classList.toggle('drop-target',!uploadState);
};
explorer.ondragleave=(e)=>{
  if (!explorer.contains(e.relatedTarget)) explorer.classList.remove('drop-target');
};
explorer.ondrop=(e)=>{
  explorer.classList.remove('drop-target');
  if (!e.dataTransfer.types.includes('Files')) return;
  e.preventDefault();
  const path=targetDirectory();
  const items=[...e.dataTransfer.items].filter(item=>item.kind==='file').map(item=>({
    entry:item.webkitGetAsEntry?.(),file:item.getAsFile()
  }));
  uploadEntries(path,state=>items.length?droppedEntries(items,state):pickedEntries([...e.dataTransfer.files],false));
};
$('refresh').onclick=()=>refresh(true);
$('settings').innerHTML=icon('settings');
$('settings').onclick=openSettings;
$('open-grid').innerHTML=icon('grid');
updateGridButton();
$('open-grid').onclick=()=>{
  if (gridPath===null) {openGrid();return;}
  gridPath=null;
  gridPreview?.abort();
  updateGridButton();
  setEmpty('emptyTitle','emptyHint');
};
$('select-many').onclick=()=>{
  selecting=!selecting;
  if (!selecting) marked.clear();
  renderTree();
};
$('bulk-delete').onclick=deleteMarked;
$('more').onclick=()=>{if (current) itemMenu(current,$('more'));};
search.oninput=()=>renderTree();
document.addEventListener('pointerdown',(e)=>{if (popup && !menu.contains(e.target) && !popup.anchor.contains(e.target)) closeMenu(false);});
document.addEventListener('keydown',(e)=>{
  if (e.key!=='Escape') return;
  if (isPreviewFullscreen()) leavePreviewFullscreen();
  else if (popup) {e.preventDefault();closeMenu();}
  else if (workspace.classList.contains('drawer') && !dialog.open) {setDrawer(false);$('toggle-tree').focus();}
});
menu.onkeydown=(e)=>{
  const buttons=[...menu.querySelectorAll('button')];
  const n=buttons.indexOf(document.activeElement);
  if (e.key==='ArrowDown'||e.key==='ArrowUp') {
    e.preventDefault();
    buttons[(n+(e.key==='ArrowDown'?1:buttons.length-1))%buttons.length]?.focus();
  }
  if (e.key==='Tab') closeMenu();
};

const fullLabel=document.createElement('div');
fullLabel.className='focus-tooltip';
fullLabel.hidden=true;
document.body.append(fullLabel);
document.addEventListener('focusin',(e)=>{
  const el=e.target;
  const clipped=el.matches('.tree-main,.tile-open')?el.querySelector('.label,.truncate'):el;
  if (!el.matches(':focus-visible') || !el.title || !clipped || clipped.scrollWidth<=clipped.clientWidth) return;
  fullLabel.textContent=el.title;
  fullLabel.hidden=false;
  const rect=el.getBoundingClientRect();
  fullLabel.style.left=Math.max(5,Math.min(rect.left,innerWidth-fullLabel.offsetWidth-5))+'px';
  fullLabel.style.top=Math.min(rect.bottom+4,innerHeight-fullLabel.offsetHeight-5)+'px';
});
document.addEventListener('focusout',()=>{fullLabel.hidden=true;});
const divider=$('divider');
let width=Number(localStorage.getItem('fm-side')) || 260;
function resize(next,persist=false) {
  width=Math.round(Math.min(540,innerWidth-305,Math.max(195,next)));
  width=Math.max(195,width);
  document.documentElement.style.setProperty('--side',width+'px');
  divider.setAttribute('aria-valuenow',width);
  if (persist) localStorage.setItem('fm-side',String(width));
}
divider.onpointerdown=(e)=>{divider.setPointerCapture(e.pointerId);e.preventDefault();};
divider.onpointermove=(e)=>{if (divider.hasPointerCapture(e.pointerId)) resize(e.clientX,true);};
divider.onkeydown=(e)=>{
  const step=e.key==='ArrowRight'?12:e.key==='ArrowLeft'?-12:0;
  if (step) {e.preventDefault();resize(width+step,true);}
};
window.addEventListener('resize',()=>{resize(width);if (popup) renderMenu();});
resize(width);
translate(document);
json('/api/session').then(showWorkspace).catch(e=>showLogin(e.error==='unauthenticated'?'':e.error||'network'));
