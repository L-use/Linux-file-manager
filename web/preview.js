import {icon} from './icons.js';
import {t,translate} from './i18n.js';

const heif = new Set(['heic','heics','heif','hif']);
const raster = new Set(['tif','tiff','dng']);
const audioFormats = new Set(['mp3','m4a','m4b','aac','wav','flac','ogg','oga','opus']);
const archives = new Set(['zip','tar','tgz','7z','rar']);
const image = new Set(['svg',...raster,'jpg','jpeg','png','webp','gif','avif','bmp',...heif]);
const video = new Set(['mp4','m4v','webm','mov','mkv','ogv','avi','divx','wmv','asf','mpeg','mpg','mpe','vob','mts','m2ts','flv','f4v','3gp','3g2','rm','rmvb']);
const codes = {
  js:'javascript',mjs:'javascript',cjs:'javascript',ts:'typescript',jsx:'javascript',tsx:'typescript',py:'python',go:'go',rs:'rust',java:'java',kt:'kotlin',
  c:'c',h:'c',cc:'cpp',cpp:'cpp',hpp:'cpp',cs:'csharp',sql:'sql',rb:'ruby',php:'php',sh:'bash',zsh:'bash',bash:'bash',
  html:'xml',htm:'xml',xml:'xml',svg:'xml',css:'css',scss:'scss',json:'json',yaml:'yaml',yml:'yaml',ini:'ini',toml:'ini',
  diff:'diff',patch:'diff',swift:'swift',lua:'lua',pl:'perl',r:'r',ps1:'powershell',dockerfile:'dockerfile',makefile:'makefile',md:'markdown',markdown:'markdown'
};
const text = new Set(['txt','log','conf','env','cfg','properties','text','gitignore','gitattributes','editorconfig','rst','out','nfo']);
export function fileType(entry) {
  if (entry.kind==='directory') return {style:'folder',icon:'folder'};
  if (entry.kind==='symlink') return {style:'special',icon:'link'};
  const lower=entry.name.toLowerCase();
  const ext=lower.includes('.')?lower.split('.').at(-1):lower;
  if (image.has(ext)) return {style:'image',icon:'image',ext};
  if (audioFormats.has(ext)) return {style:'audio',icon:'audio',ext};
  if (archives.has(ext) || lower.endsWith('.tar.gz')) return {style:'archive',icon:'archive',ext};
  if (ext==='pptx') return {style:'slides',icon:'slides',ext};
  if (video.has(ext) || entry.media==='video/mp2t') return {style:'video',icon:'video',ext};
  if (ext==='pdf') return {style:'pdf',icon:'text',ext};
  if (ext==='docx') return {style:'doc',icon:'text',ext};
  if (ext==='xlsx'||ext==='csv') return {style:'sheet',icon:'table',ext};
  if (Object.hasOwn(codes,ext)) return {style:'code',icon:'code',ext};
  if (text.has(ext)||['readme','license','changelog','authors','notice'].includes(ext)) return {style:'text',icon:'text',ext};
  return {style:'file',icon:'file',ext};
}
const node = (tag,cls='') => Object.assign(document.createElement(tag),{className:cls});
const clamp = (value,min,max) => Math.max(min,Math.min(max,value));
const scripts=new Map();
function script(source) {
  if (!scripts.has(source)) scripts.set(source,new Promise((resolve,reject)=>{
    const element=node('script');
    element.src=source;
    element.onload=resolve;
    element.onerror=()=>{scripts.delete(source);element.remove();reject(new Error('script unavailable'));};
    document.head.append(element);
  }));
  return scripts.get(source);
}
function toolbar() {return node('div','preview-tools');}
function badge(key) {
  const output=node('p','notice');
  output.dataset.i18n=key;
  return output;
}
function caption(b,key) {
  b.dataset.label=key;
  b.title=t(key);
  b.setAttribute('aria-label',b.title);
}
function setPressed(button,on) {button.setAttribute('aria-pressed',String(on));}
function waitLive(ctx) {ctx.signal.throwIfAborted();}
export function mountPreview(ctx) {
  switch(ctx.type.style) {
    case 'image': return media(ctx,false,heif.has(ctx.type.ext)?blob=>heifImage(ctx,blob):null);
    case 'audio': return audioPreview(ctx);
    case 'archive': return archiveView(ctx);
    case 'slides': return presentationView(ctx);
    case 'video': return media(ctx,true,()=>ctx.convertVideo().then(url=>({url})));
    case 'text': case 'code': return source(ctx);
    case 'pdf': return pdf(ctx);
    case 'doc': return word(ctx);
    case 'sheet': return spreadsheet(ctx);
    default:ctx.report('unsupported');return null;
  }
}

function media(ctx,isVideo,fallback) {
  const view=node('div','media-view'), tools=toolbar(), stage=node('div','media-stage'+(isVideo?' video':''));
  let object=node(isVideo?'video':'img','media-object');
  const controls=node('div','media-controls');
  const output=node('output'), pointers=new Map();
  let fullscreen=ctx.fullscreenActive(), beforeFullscreen;
  let mode='fit', zoom=1, x=0, y=0, naturalWidth=0, naturalHeight=0, previousPinch;
  let attemptedFallback=false, disposed=false, frame=0, bufferTimer;
  let stageWidth=0,stageHeight=0,layoutScale=0;
  const useThumbnail=!isVideo && ['jpg','jpeg','png'].includes(ctx.type.ext) && ctx.entry.size>=1024*1024;
  const previewRequest=useThumbnail?new AbortController():null;
  let thumbnailURL='', originalURL='', thumbnailSize, original, originalRequested=false, originalBlob;
  const originalStatus=node('div','media-conversion original-status');
  const originalLabel=node('span'), originalProgress=node('progress'), originalPercent=node('span');
  originalLabel.dataset.i18n='loadingOriginal';
  originalProgress.dataset.label='loadingOriginal';
  originalProgress.max=1;
  originalStatus.append(originalLabel,originalProgress,originalPercent);
  const conversion=node('span','media-conversion');
  conversion.dataset.i18n=isVideo?'convertingVideo':'decodingImage';
  conversion.setAttribute('role','status');
  const base=()=>Math.min(fullscreen?Infinity:1,stageWidth/naturalWidth,stageHeight/naturalHeight);
  const max=()=>Math.max(8,base()*24);
  function paint() {
    if (!naturalWidth || !stageWidth || !stageHeight) return;
    if (mode==='fit') {zoom=base();x=0;y=0;}
    if (mode==='fill') zoom=Math.max(stageWidth/naturalWidth,stageHeight/naturalHeight);
    if (mode==='actual') zoom=1;
    const dw=naturalWidth*zoom,dh=naturalHeight*zoom;
    x=clamp(x,-Math.max(0,(dw-stageWidth)/2),Math.max(0,(dw-stageWidth)/2));
    y=clamp(y,-Math.max(0,(dh-stageHeight)/2),Math.max(0,(dh-stageHeight)/2));

    // At fit size the element only needs the dimensions of the visible frame.
    const scale=Math.min(zoom,1);
    if (scale!==layoutScale) {
      layoutScale=scale;
      object.style.width=naturalWidth*scale+'px';
      object.style.height=naturalHeight*scale+'px';
    }
    object.style.transform='translate(calc(-50% + '+x+'px),calc(-50% + '+y+'px)) scale('+zoom/scale+')';
    object.classList.add('ready');
    stage.classList.toggle('zoomed',dw>stageWidth+.5||dh>stageHeight+.5);
    const percentage=Math.max(1,Math.round(100*zoom))+'%';
    if (output.value!==percentage) output.value=percentage;
  }
  function schedulePaint() {
    if (frame || disposed) return;
    frame=requestAnimationFrame(()=>{
      frame=0;
      paint();
    });
  }
  function zoomMode(next) {
    mode=next;
    if (mode==='actual') zoom=1;
    if (mode==='fit' && naturalWidth && stageWidth && stageHeight) zoom=base();
    setPressed(fit,mode==='fit');
    if (actual) setPressed(actual,mode==='actual');
    setPressed(fill,mode==='fill');
    schedulePaint();
  }
  function apply(scale,clientX,clientY,oldX=clientX,oldY=clientY) {
    if (scale>zoom) requestOriginal();
    if (!naturalWidth || !stageWidth || !stageHeight) return;
    const rect=stage.getBoundingClientRect();
    const originX=rect.left+rect.width/2,originY=rect.top+rect.height/2;
    const next=clamp(scale,Math.max(.01,Math.min(base()/2,.125)),max());
    const factor=next/zoom;
    x=(clientX-originX)-(oldX-originX-x)*factor;
    y=(clientY-originY)-(oldY-originY-y)*factor;
    zoom=next;
    zoomMode('custom');
  }
  function step(factor) {
    const r=stage.getBoundingClientRect();
    apply(zoom*factor,r.left+r.width/2,r.top+r.height/2);
  }
  const fit=ctx.button('fit','fit',()=>zoomMode('fit'),'button',true);
  const fill=ctx.button('fillScreen','fullscreen',()=>{x=0;y=0;zoomMode('fill');},'button');
  fill.hidden=!fullscreen;
  const actual=isVideo?null:ctx.button('actual','image',()=>{requestOriginal();x=0;y=0;zoomMode('actual');},'button');
  if (actual) actual.textContent='100%';
  tools.append(ctx.button('zoomOut','minus',()=>step(1/1.25)),output,ctx.button('zoomIn','plus',()=>step(1.25)),fit);
  if (actual) tools.append(actual);
  tools.append(fill);
  if (ctx.type.ext==='svg') tools.append(ctx.button('source','code',()=>ctx.showAs('source'),'button',true));
  view.append(tools,stage);
  stage.append(object);
  output.textContent='—';
  const pending=node('span','spinner media-spinner');
  pending.setAttribute('role','status');
  pending.dataset.label='loading';
  stage.append(pending);
  function loading(active) {
    clearTimeout(bufferTimer);
    pending.hidden=!active;
    stage.setAttribute('aria-busy',String(active || conversion.isConnected || originalStatus.isConnected));
  }
  loading(true);
  function dimensions(width,height) {
    if (!width || !height) return;
    naturalWidth=width;
    naturalHeight=height;
    layoutScale=0;
    ctx.details(width+' × '+height);
    schedulePaint();
  }
  function frameReady() {
    conversion.remove();
    originalStatus.remove();
    originalBlob=null;
    loading(false);
  }
  function dropThumbnail() {
    if (thumbnailURL) URL.revokeObjectURL(thumbnailURL);
    thumbnailURL='';
    thumbnailSize=null;
  }
  async function loadOriginal(image) {
    stage.append(originalStatus);
    translate(stage);
    loading(false);
    originalProgress.removeAttribute('value');
    originalPercent.textContent='';
    try {
      if (ctx.entry.size>96*1024*1024) {
        image.src=ctx.url;
        return;
      }
      const reply=await ctx.read();
      const total=Number(reply.headers.get('Content-Length'));
      const mime=reply.headers.get('Content-Type') || '';
      const reader=reply.body?.getReader();
      const parts=[];
      let loaded=0;
      if (reader) {
        for (;;) {
          const {done,value}=await reader.read();
          if (done) break;
          parts.push(value);
          loaded+=value.byteLength;
          if (total>0) {
            originalProgress.value=Math.min(1,loaded/total);
            originalPercent.textContent=Math.floor(originalProgress.value*100)+'%';
          }
        }
      }
      const blob=reader?new Blob(parts,{type:mime}):await reply.blob();
      if (disposed) return;
      if (!isVideo && fallback) originalBlob=blob;
      originalURL=URL.createObjectURL(blob);
      image.src=originalURL;
    } catch(error) {
      if (disposed || ctx.signal.aborted) return;
      if (image===object && error?.error) ctx.report(error.error);
      else image.onerror?.();
    }
  }
  function requestOriginal() {
    if (!useThumbnail || originalRequested || disposed) return;
    originalRequested=true;
    previewRequest.abort();
    if (!thumbnailSize || !naturalWidth) {
      dropThumbnail();
      loadOriginal(object);
      return;
    }
    const image=node('img','media-object');
    image.alt=ctx.entry.name;
    image.decoding='async';
    image.fetchPriority='high';
    image.draggable=false;
    original=image;
    image.onload=()=>{
      if (disposed) return;
      const small=object;
      object=image;
      dimensions(image.naturalWidth,image.naturalHeight);
      paint();
      small.replaceWith(image);
      original=null;
      dropThumbnail();
      frameReady();
    };
    image.onerror=()=>{
      if (disposed) return;
      if (originalURL) URL.revokeObjectURL(originalURL);
      originalURL='';
      original=null;
      originalRequested=false;
      frameReady();
      ctx.notify({error:'originalError'});
    };
    loadOriginal(image);
  }
  async function loadThumbnail() {
    try {
      const edge=innerWidth<780?800:innerWidth>1900?1600:1280;
      const asset=await ctx.thumbnail(edge,previewRequest.signal);
      if (disposed || previewRequest.signal.aborted) return;
      thumbnailSize=asset;
      thumbnailURL=URL.createObjectURL(asset.blob);
      object.src=thumbnailURL;
    } catch(error) {
      if (disposed || previewRequest.signal.aborted) return;
      if (['permission','protected','missing','boundary','unauthenticated'].includes(error?.error)) ctx.report(error.error);
      else requestOriginal();
    }
  }
  const time=(s)=>{
    s=Math.floor(Number.isFinite(s)?s:0);
    const minutes=Math.floor(s/60);
    return minutes+':'+String(s%60).padStart(2,'0');
  };
  let updateLabels=()=>{};
  if (isVideo) {
    view.append(controls);
    object.playsInline=true;
    object.preload='auto';
    const toggle=()=>object.paused?object.play().catch(()=>{}):object.pause();
    const play=ctx.button('play','play',toggle), mute=ctx.button('mute','volume',()=>{object.muted=!object.muted;});
    const seek=node('input','seek'), volume=node('input','volume'), clock=node('output');
    seek.type=volume.type='range';
    seek.min=volume.min='0';seek.step='any';volume.step='.02';volume.max='1';seek.max='0';seek.value='0';volume.value='1';
    seek.dataset.label='progress';volume.dataset.label='volume';
    seek.disabled=true;
    const seekTrack=node('div','seek-track'), timeline=node('div','seek-timeline');
    const buffered=node('div','seek-buffered'), played=node('div','seek-played');
    timeline.setAttribute('aria-hidden','true');
    timeline.append(buffered,played);
    seekTrack.append(timeline,seek);
    controls.append(play,seekTrack,clock,mute,volume);
    let previousRanges='';
    function paintBuffer(duration) {
      const ranges=[];
      if (Number.isFinite(duration) && duration>0) {
        for (let i=0;i<object.buffered.length;i++) {
          ranges.push([100*object.buffered.start(i)/duration,100*object.buffered.end(i)/duration]);
        }
      }
      const key=JSON.stringify(ranges);
      if (key===previousRanges) return;
      previousRanges=key;
      buffered.replaceChildren(...ranges.map(([start,end])=>{
        const segment=node('span');
        segment.style.left=start+'%';
        segment.style.width=(end-start)+'%';
        return segment;
      }));
    }
    function status() {
      const duration=object.duration;
      if (Number.isFinite(duration)) {
        const limit=String(duration);
        if (seek.max!==limit) seek.max=limit;
        seek.disabled=duration===0;
        const position=String(object.currentTime);
        if (seek.value!==position) seek.value=position;
      }
      played.style.width=Number.isFinite(duration)&&duration>0?100*object.currentTime/duration+'%':'0%';
      paintBuffer(duration);
      const label=time(object.currentTime)+' / '+time(duration);
      if (clock.value!==label) clock.value=label;
    }
    updateLabels=()=>{
      play.innerHTML=icon(object.paused?'play':'pause');
      caption(play,object.paused?'play':'pause');
      mute.innerHTML=icon(object.muted||object.volume===0?'mute':'volume');
      caption(mute,object.muted?'unmute':'mute');
    };
    object.onloadedmetadata=()=>{dimensions(object.videoWidth,object.videoHeight);status();};
    object.onresize=()=>dimensions(object.videoWidth,object.videoHeight);
    object.onloadeddata=object.oncanplay=object.onplaying=frameReady;
    object.onwaiting=()=>{
      clearTimeout(bufferTimer);
      bufferTimer=setTimeout(()=>loading(true),150);
    };
    object.onseeked=()=>{
      if (object.readyState>=2) frameReady();
    };
    object.ontimeupdate=object.ondurationchange=object.onprogress=object.onemptied=status;
    object.onplay=object.onpause=updateLabels;
    object.onvolumechange=()=>{volume.value=String(object.muted?0:object.volume);updateLabels();};
    seek.oninput=()=>{object.currentTime=Number(seek.value);status();};
    volume.oninput=()=>{object.volume=Number(volume.value);object.muted=false;};
    status();
    updateLabels();
  } else {
    object.alt=ctx.entry.name;
    object.decoding='async';
    object.fetchPriority='high';
    object.draggable=false;
    object.onload=()=>{
      dimensions(thumbnailSize?.width || object.naturalWidth,thumbnailSize?.height || object.naturalHeight);
      frameReady();
    };
  }
  const resize=new ResizeObserver(([entry])=>{
    stageWidth=entry.contentRect.width;
    stageHeight=entry.contentRect.height;
    schedulePaint();
  });
  resize.observe(stage);
  stage.onwheel=(e)=>{if (naturalWidth) {e.preventDefault();apply(zoom*(e.deltaY<0?1.13:1/1.13),e.clientX,e.clientY);}};
  const pinch=()=>{
    if (pointers.size<2) return null;
    const [a,b]=[...pointers.values()];
    return {x:(a.x+b.x)/2,y:(a.y+b.y)/2,d:Math.hypot(a.x-b.x,a.y-b.y)};
  };
  stage.onpointerdown=(e)=>{
    if (e.button!==0) return;
    stage.setPointerCapture(e.pointerId);
    pointers.set(e.pointerId,{x:e.clientX,y:e.clientY});
    previousPinch=pinch();
  };
  stage.onpointermove=(e)=>{
    const prior=pointers.get(e.pointerId);
    if (!prior) return;
    pointers.set(e.pointerId,{x:e.clientX,y:e.clientY});
    const next=pinch();
    if (next && previousPinch?.d) {
      apply(zoom*next.d/previousPinch.d,next.x,next.y,previousPinch.x,previousPinch.y);
    } else if (stage.classList.contains('zoomed')) {
      stage.classList.add('panning');
      x+=e.clientX-prior.x;y+=e.clientY-prior.y;
      schedulePaint();
    }
    previousPinch=next;
  };
  const end=(e)=>{pointers.delete(e.pointerId);previousPinch=pinch();stage.classList.remove('panning');};
  stage.onpointerup=stage.onpointercancel=stage.onlostpointercapture=end;
  stage.ondblclick=ctx.fullscreen;
  function destroy() {
    if (disposed) return;
    disposed=true;
    resize.disconnect();
    cancelAnimationFrame(frame);
    clearTimeout(bufferTimer);
    previewRequest?.abort();
    original?.removeAttribute('src');
    if (originalURL) URL.revokeObjectURL(originalURL);
    originalBlob=null;
    object.removeAttribute('src');
    dropThumbnail();
    if (isVideo) {object.pause();object.load();}
    else if (object instanceof HTMLCanvasElement) object.width=object.height=0;
  }
  object.onerror=async()=>{
    if (ctx.signal.aborted || disposed || conversion.isConnected) return;
    if (thumbnailURL && !originalRequested) {
      dropThumbnail();
      requestOriginal();
      return;
    }
    let error='mediaError';
    try {
      if (fallback && !attemptedFallback) {
        attemptedFallback=true;
        originalStatus.remove();
        stage.append(conversion);
        loading(false);
        translate(stage);
        const asset=await fallback(originalBlob);
        originalBlob=null;
        if (ctx.signal.aborted || disposed) {
          if (asset.canvas) asset.canvas.width=asset.canvas.height=0;
          return;
        }
        if (asset.canvas) {
          object.replaceWith(asset.canvas);
          object=asset.canvas;
          dimensions(object.width,object.height);
          frameReady();
        } else {
          conversion.remove();
          loading(true);
          object.src=asset.url;
        }
        return;
      }
      // The fallback requests also validate access, so only probe an unresolved error.
      await ctx.probeMedia();
      if (ctx.signal.aborted || disposed) return;
    } catch (e) {
      if (ctx.signal.aborted || disposed) return;
      error=e.error || 'mediaError';
    }
    destroy();
    ctx.report(error);
  };
  ctx.host.replaceChildren(view);
  zoomMode(mode);
  if (useThumbnail && fullscreen) requestOriginal();
  else if (useThumbnail) loadThumbnail();
  else if (isVideo || ctx.type.ext==='svg') object.src=ctx.url;
  else loadOriginal(object);
  return {destroy,fullscreenChanged(active){
    if (active===fullscreen) return;
    fullscreen=active;
    fill.hidden=!active;
    if (active) {
      beforeFullscreen={mode,zoom,x,y};
      x=0;y=0;
      zoomMode('fit');
      requestOriginal();
    } else if (beforeFullscreen) {
      ({zoom,x,y}=beforeFullscreen);
      zoomMode(beforeFullscreen.mode);
      beforeFullscreen=null;
    } else zoomMode('fit');
  },language(){
    translate(view);
    updateLabels();
    if (naturalWidth) ctx.details(naturalWidth+' × '+naturalHeight);
  }};
}

async function sanitizer(html) {
  await script('/vendor/dompurify/dist/purify.min.js');
  const fragment=window.DOMPurify.sanitize(html,{
    RETURN_DOM_FRAGMENT:true,
    ALLOWED_TAGS:['a','p','br','em','strong','del','s','u','b','i','sub','sup','h1','h2','h3','h4','h5','h6','ul','ol','li','hr','blockquote','pre','code','span','div','img','table','thead','tbody','tfoot','tr','th','td','dl','dt','dd'],
    ALLOWED_ATTR:['href','id','src','alt','title','rowspan','colspan','start'],
    ALLOW_DATA_ATTR:false
  });
  for (const a of fragment.querySelectorAll('a[href]')) {
    if (!a.getAttribute('href').startsWith('#')) a.removeAttribute('href');
  }
  for (const img of fragment.querySelectorAll('img[src]')) {
    if (!/^data:image\/(?:png|jpeg|gif|webp|bmp);base64,/i.test(img.getAttribute('src'))) img.removeAttribute('src');
  }
  return fragment;
}
async function source(ctx) {
  let data;
  try {data=await ctx.getText();}
  catch(error) {
    if (error?.error==='encoding') return encodingFailure(ctx);
    throw error;
  }
  waitLive(ctx);
  const tools=toolbar(), scroll=node('div','preview-scroll code-scroll'), lines=node('ol','code-lines');
  const normalized=data.text.replace(/\r\n?/g,'\n');
  const extension=ctx.type.ext;
  const isMarkdown=extension==='md'||extension==='markdown';
  let wrap=false, rendered=false, parsed=null;
  const toggleWrap=ctx.button('wrap','wrap',()=>{
    wrap=!wrap;
    setPressed(toggleWrap,wrap);
    lines.classList.toggle('wrap',wrap);
    editor.wrap=wrap?'soft':'off';
  },'button',true);
  const toggleMarkdown=ctx.button('markdown','text',async()=>{
    if (!parsed) {
      try {
        const {marked}=await import('/vendor/marked/lib/marked.esm.js');
        parsed=await sanitizer(marked.parse(normalized));
        waitLive(ctx);
      } catch(e) {if (!ctx.signal.aborted) ctx.report('readError');return;}
    }
    rendered=!rendered;
    showSource();
    const label=rendered?'source':'markdown';
    toggleMarkdown.dataset.label=label;
    toggleMarkdown.querySelector('span').dataset.i18n=label;
    translate(tools);
  },'button',true);
  tools.append(toggleWrap,encodingSelect(ctx,data.encoding));
  if (extension==='svg') tools.append(ctx.button('preview','image',()=>ctx.showAs('image'),'button',true));
  if (isMarkdown) tools.append(toggleMarkdown);
  let highlighting;
  if (Object.hasOwn(codes,extension) && data.text.length<300000) {
    try {
      await script('/vendor/cdn-assets/highlight.min.js');
      if (window.hljs.getLanguage(codes[extension])) highlighting=window.hljs.highlight(normalized,{language:codes[extension]}).value;
    } catch {}
    waitLive(ctx);
  }
  if (highlighting) {
    // Split a highlighted fragment by line while preserving spans that cross line boundaries.
    const template=node('template');
    template.innerHTML=highlighting;
    const stack=[];
    let line=node('li'), target=line;
    function newline() {
      lines.append(line);
      line=node('li');
      target=line;
      for (const span of stack) {const clone=span.cloneNode(false);target.append(clone);target=clone;}
    }
    function visit(n) {
      if (n.nodeType===Node.TEXT_NODE) n.data.split('\n').forEach((part,index)=>{
        if (index) newline();
        if (part) target.append(part);
      });
      else if (n.nodeName==='SPAN') {
        stack.push(n);
        const span=n.cloneNode(false);
        target.append(span);
        target=span;
        n.childNodes.forEach(visit);
        target=target.parentNode;
        stack.pop();
      }
    }
    template.content.childNodes.forEach(visit);
    lines.append(line);
  } else {
    for (const line of normalized.split('\n')) {
      const li=node('li');
      li.textContent=line;
      lines.append(li);
    }
  }
  const editor=node('textarea','code-editor');
  editor.dataset.label='codeEditor';
  editor.spellcheck=false;
  editor.autocapitalize='off';
  editor.wrap='off';
  editor.value=data.text;
  const original=editor.value;
  let editing=false, saving=false;
  const dirty=()=>editing && editor.value!==original;
  const beforeUnload=(e)=>{e.preventDefault();e.returnValue='';};
  const editButton=ctx.button('editCode','edit',startEditing,'button',true);
  const saveButton=ctx.button('save','check',save,'primary',true);
  const cancelButton=ctx.button('cancel','close',()=>{
    if (dirty() && !window.confirm(t('discardChanges'))) return;
    editing=false;
    editor.value=original;
    syncEditor();
    showSource();
    editButton.focus();
  },'button',true);
  tools.append(editButton,saveButton,cancelButton);
  function syncEditor() {
    editButton.hidden=editing || data.truncated || data.encoding!=='UTF-8';
    toggleMarkdown.hidden=editing || !isMarkdown;
    toggleWrap.hidden=!editing && rendered;
    saveButton.hidden=cancelButton.hidden=!editing;
    saveButton.disabled=saving || !dirty();
    cancelButton.disabled=saving;
    editor.readOnly=saving;
    if (dirty()) window.addEventListener('beforeunload',beforeUnload);
    else window.removeEventListener('beforeunload',beforeUnload);
  }
  function showSource() {
    scroll.classList.toggle('code-scroll',!rendered);
    scroll.classList.remove('editing-code');
    toggleWrap.hidden=rendered;
    if (rendered) {
      const article=node('article','document');
      article.append(parsed.cloneNode(true));
      scroll.replaceChildren(article);
    } else scroll.replaceChildren(lines);
  }
  function startEditing() {
    editing=true;
    scroll.classList.add('code-scroll','editing-code');
    scroll.replaceChildren(editor);
    syncEditor();
    translate(scroll);
    editor.focus();
  }
  async function save() {
    if (saving || !dirty()) return false;
    saving=true;
    syncEditor();
    try {
      const endings=data.text.match(/\r\n|\r|\n/g);
      const ending=endings?.every(item=>item===endings[0])?endings[0]:'\n';
      const value=ending==='\n'?editor.value:editor.value.replaceAll('\n',ending);
      await ctx.saveText((data.bom?'\uFEFF':'')+value,data.version);
      editing=false;
      syncEditor();
      await ctx.saved();
      return true;
    } catch(error) {
      if (!ctx.signal.aborted) {
        ctx.notify(error);
        editor.focus();
      }
      return false;
    } finally {
      saving=false;
      if (!ctx.signal.aborted) syncEditor();
    }
  }
  editor.oninput=syncEditor;
  editor.onkeydown=(e)=>{
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase()==='s') {
      e.preventDefault();
      save();
    }
  };
  const note=node('p','notice');
  const labels=()=>{
    note.textContent=t('previewBytes',{bytes:ctx.size(data.bytes),total:ctx.size(data.total)});
    ctx.details(data.encoding);
  };
  syncEditor();
  showSource();
  ctx.host.replaceChildren(tools,...(data.truncated?[note]:[]),scroll);
  labels();
  return {language:labels,dirty,save,saving:()=>saving,destroy(){window.removeEventListener('beforeunload',beforeUnload);}};
}

async function arrayBuffer(ctx,max) {
  if (ctx.entry.size>max) throw {error:'docLimit'};
  const reply=await ctx.read();
  const reader=reply.body.getReader();
  const length=Number(reply.headers.get('Content-Length'));
  if (length>max) {await reader.cancel();throw {error:'docLimit'};}
  const chunks=[];
  let total=0;
  for (;;) {
    const {done,value}=await reader.read();
    if (done) break;
    total+=value.byteLength;
    if (total>max) {await reader.cancel();throw {error:'docLimit'};}
    chunks.push(value);
  }
  waitLive(ctx);
  const bytes=new Uint8Array(total);
  let offset=0;
  for (const chunk of chunks) {bytes.set(chunk,offset);offset+=chunk.byteLength;}
  return bytes.buffer;
}

async function heifImage(ctx,blob) {
  const bytes=blob?await blob.arrayBuffer():await arrayBuffer(ctx,96*1024*1024);
  waitLive(ctx);
  const worker=new Worker('/heif-worker.js',{type:'module'});
  const decoded=await new Promise((resolve,reject)=>{
    function finish(callback,value) {
      clearTimeout(timeout);
      ctx.signal.removeEventListener('abort',abort);
      worker.terminate();
      callback(value);
    }
    const abort=()=>finish(reject,ctx.signal.reason);
    const timeout=setTimeout(()=>finish(reject,{error:'mediaError'}),45000);
    ctx.signal.addEventListener('abort',abort,{once:true});
    worker.onerror=()=>finish(reject,{error:'mediaError'});
    worker.onmessage=({data})=>data.error?finish(reject,data):finish(resolve,data);
    worker.postMessage(bytes,[bytes]);
  });
  if (ctx.signal.aborted) {
    decoded.bitmap?.close();
    waitLive(ctx);
  }
  const canvas=node('canvas','media-object');
  canvas.setAttribute('role','img');
  canvas.setAttribute('aria-label',ctx.entry.name);
  canvas.width=decoded.width;
  canvas.height=decoded.height;
  if (decoded.bitmap) {
    const renderer=canvas.getContext('bitmaprenderer');
    if (renderer) renderer.transferFromImageBitmap(decoded.bitmap);
    else {
      try {canvas.getContext('2d').drawImage(decoded.bitmap,0,0);}
      finally {decoded.bitmap.close();}
    }
  } else {
    canvas.getContext('2d').putImageData(new ImageData(decoded.data,decoded.width,decoded.height),0,0);
  }
  return {canvas};
}

function verifyOfficeZip(buffer) {
  const data=new DataView(buffer);
  let end=data.byteLength-22;
  while (end>=Math.max(0,data.byteLength-65557) && data.getUint32(end,true)!==0x06054b50) end--;
  if (end<0 || end<buffer.byteLength-65557) throw {error:'readError'};
  const entries=data.getUint16(end+10,true);
  const central=data.getUint32(end+16,true);
  if (entries>12000 || central>=end) throw {error:'docLimit'};
  let total=0,pos=central;
  for (let i=0;i<entries;i++) {
    if (pos+46>end || data.getUint32(pos,true)!==0x02014b50) throw {error:'readError'};
    const expanded=data.getUint32(pos+24,true);
    total+=expanded;
    if (total>192*1024*1024 || expanded>64*1024*1024) throw {error:'docLimit'};
    pos+=46+data.getUint16(pos+28,true)+data.getUint16(pos+30,true)+data.getUint16(pos+32,true);
  }
}

async function word(ctx) {
  const buffer=await arrayBuffer(ctx,48*1024*1024);
  verifyOfficeZip(buffer);
  await script('/vendor/mammoth/mammoth.browser.min.js');
  waitLive(ctx);
  const result=await window.mammoth.convertToHtml({arrayBuffer:buffer},{externalFileAccess:false});
  const content=await sanitizer(result.value);
  waitLive(ctx);
  const scroll=node('div','preview-scroll'), documentBody=node('article','document');
  documentBody.append(content);
  scroll.append(documentBody);
  const tools=toolbar(), previewNote=badge('docFormat'), editNote=badge('wordEditingHint');
  const fields=node('div','word-editor');
  fields.setAttribute('role','group');
  fields.dataset.label='wordEditor';
  const changes=new Map();
  let data, loading=false, editing=false, saving=false;
  const dirty=()=>changes.size>0;
  const beforeUnload=(event)=>{event.preventDefault();event.returnValue='';};
  const editButton=ctx.button('editCode','edit',edit,'button',true);
  const saveButton=ctx.button('save','check',save,'primary',true);
  const cancelButton=ctx.button('cancel','close',()=>{
    if (dirty() && !window.confirm(t('discardChanges'))) return;
    changes.clear();
    showPreview();
    editButton.focus();
  },'button',true);
  tools.append(editButton,saveButton,cancelButton);

  function sync() {
    editButton.hidden=editing;
    editButton.disabled=loading;
    saveButton.hidden=cancelButton.hidden=!editing;
    saveButton.disabled=saving || !dirty();
    cancelButton.disabled=saving;
    fields.inert=saving;
    if (dirty()) window.addEventListener('beforeunload',beforeUnload);
    else window.removeEventListener('beforeunload',beforeUnload);
  }
  function showPreview() {
    editing=false;
    sync();
    ctx.host.replaceChildren(tools,previewNote,scroll);
  }
  async function edit() {
    loading=true;
    sync();
    try {
      data=await ctx.getWord();
      waitLive(ctx);
      if (!data.paragraphs.length) throw {error:'wordNothingEditable'};
      const inputs=data.paragraphs.map((text,index)=>{
        const control=node('input');
        control.type='text';
        control.value=text;
        const label=node('label');
        const caption=node('span');
        const kind=node('span');
        kind.dataset.i18n='wordParagraph';
        caption.append(kind,' '+(index+1));
        label.append(caption,control);
        control.oninput=()=>{
          if (control.value===text) changes.delete(index);
          else changes.set(index,control.value);
          sync();
        };
        return label;
      });
      fields.replaceChildren(...inputs);
      editing=true;
      ctx.host.replaceChildren(tools,editNote,fields);
      translate(ctx.host);
      inputs[0].lastChild.focus();
    } catch (error) {
      if (!ctx.signal.aborted) ctx.notify(error);
    } finally {
      loading=false;
      sync();
    }
  }
  async function save() {
    if (saving || !dirty()) return false;
    saving=true;
    sync();
    try {
      await ctx.saveWord([...changes].map(([index,text])=>({index,text})),data.version);
      changes.clear();
      sync();
      await ctx.saved();
      return true;
    } catch (error) {
      if (!ctx.signal.aborted) ctx.notify(error);
      return false;
    } finally {
      saving=false;
      sync();
    }
  }
  fields.onkeydown=(event)=>{
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase()==='s') {
      event.preventDefault();
      save();
    }
  };
  showPreview();
  return {dirty,save,saving:()=>saving,destroy(){window.removeEventListener('beforeunload',beforeUnload);}};
}

async function spreadsheet(ctx) {
  const buffer=await arrayBuffer(ctx,48*1024*1024);
  if (ctx.type.ext==='xlsx') verifyOfficeZip(buffer);
  await script('/vendor/xlsx/xlsx.full.min.js');
  waitLive(ctx);
  const XLSX=window.XLSX;
  let data=buffer;
  let csvEncoding;
  if (ctx.type.ext==='csv') {
    let decoded;
    try {decoded=decodeCSV(buffer,ctx.encoding);}
    catch(error) {
      if (error?.error==='encoding') return encodingFailure(ctx);
      throw error;
    }
    data=decoded.text;
    csvEncoding=decoded.encoding;
  }
  const canEdit=ctx.type.ext==='xlsx';
  const book=XLSX.read(data,{type:typeof data==='string'?'string':'array',raw:!canEdit,cellText:true,sheetStubs:canEdit,sheetRows:20001});
  if (!book.SheetNames.length) throw {error:'noRows'};
  const tools=toolbar(), scroll=node('div','preview-scroll table-scroll'), tabs=node('div','sheet-tabs');
  tabs.setAttribute('role','tablist');
  const rangeLabel=node('output');
  const changes=new Map();
  const dirty=()=>changes.size>0;
  const beforeUnload=(event)=>{event.preventDefault();event.returnValue='';};
  let editing=false, saving=false;
  let index=0, block=0, range=null;
  let batch=160;
  const previous=ctx.button('previous','chevron',()=>{block--;draw();});
  previous.firstChild.style.transform='rotate(180deg)';
  const next=ctx.button('next','chevron',()=>{block++;draw();});
  tools.append(previous,rangeLabel,next);
  if (csvEncoding) tools.append(encodingSelect(ctx,csvEncoding));
  const editButton=ctx.button('editCode','edit',()=>{editing=true;syncEdits();draw();},'button',true);
  const saveButton=ctx.button('save','check',save,'primary',true);
  const cancelButton=ctx.button('cancel','close',()=>{
    if (dirty() && !window.confirm(t('discardChanges'))) return;
    changes.clear();
    editing=false;
    syncEdits();
    draw();
    editButton.focus();
  },'button',true);
  const editNote=badge('sheetEditHint');
  if (canEdit) tools.append(editButton,saveButton,cancelButton);
  function syncEdits() {
    editButton.hidden=editing;
    saveButton.hidden=cancelButton.hidden=editNote.hidden=!editing;
    saveButton.disabled=saving || !dirty();
    cancelButton.disabled=saving;
    scroll.inert=saving;
    tabs.inert=saving;
    previous.disabled=saving || block===0;
    next.disabled=saving || (block+1)*batch>=amount();
    if (dirty()) window.addEventListener('beforeunload',beforeUnload);
    else window.removeEventListener('beforeunload',beforeUnload);
  }
  async function save() {
    if (saving || !dirty()) return false;
    saving=true;
    syncEdits();
    try {
      await ctx.saveSheet([...changes.values()],ctx.entry.version);
      changes.clear();
      syncEdits();
      await ctx.saved();
      return true;
    } catch (error) {
      if (!ctx.signal.aborted) ctx.notify(error);
      return false;
    } finally {
      saving=false;
      syncEdits();
    }
  }
  scroll.onkeydown=(event)=>{
    if (editing && (event.metaKey || event.ctrlKey) && event.key.toLowerCase()==='s') {
      event.preventDefault();
      save();
    }
  };
  function showCell(target,value,address,sheet) {
    const key=sheet+'\0'+address;
    const changed=changes.get(key);
    const original=String(value?.v??'');
    const display=changed?.value??value?.w??original;
    target.title=display;
    if (!editing || !value || value.f!=null || value.F || !['s','n','b','z'].includes(value.t)) {
      target.textContent=display;
      return;
    }
    const button=node('button','sheet-cell-button');
    button.type='button';
    button.textContent=display;
    button.setAttribute('aria-label',display?address+' · '+display:address);
    target.replaceChildren(button);
    button.onclick=()=>{
      const input=node('input','sheet-cell-input');
      input.type='text';
      input.maxLength=32767;
      input.setAttribute('aria-label',address);
      input.value=changes.get(key)?.value??original;
      const prior=changes.get(key);
      input.oninput=()=>{
        const text=input.value;
        if (text===original) changes.delete(key);
        else {
          let kind='text', next=text;
          if (text==='') kind='blank';
          else if (value.t!=='s' && /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(text) && Number.isFinite(Number(text))) kind='number';
          else if (value.t==='b' && /^(true|false)$/i.test(text)) {kind='boolean';next=text.toLowerCase();}
          changes.set(key,{sheet,cell:address,value:next,kind});
        }
        syncEdits();
      };
      input.onblur=()=>showCell(target,value,address,sheet);
      input.onkeydown=(event)=>{
        if (event.key!=='Enter' && event.key!=='Escape') return;
        event.preventDefault();
        if (event.key==='Escape') {
          event.stopPropagation();
          if (prior) changes.set(key,prior);
          else changes.delete(key);
          syncEdits();
        }
        input.blur();
        target.firstElementChild?.focus();
      };
      target.replaceChildren(input);
      input.focus();
      input.select();
    };
  }
  const buttons=book.SheetNames.map((name,i)=>{
    const button=node('button','button');
    button.type='button';
    button.setAttribute('role','tab');
    button.textContent=button.title=name;
    button.onclick=()=>{index=i;block=0;draw();};
    tabs.append(button);
    return button;
  });
  const note=badge('workbookLimit');
  const amount=()=>range?range.e.r-range.s.r+1:0;
  const labels=()=>{
    if (!range) rangeLabel.textContent=t('noRows');
    else rangeLabel.textContent=t('rowRange',{first:range.s.r+block*batch+1,last:Math.min(range.e.r+1,range.s.r+(block+1)*batch),total:range.e.r+1});
  };
  function draw() {
    const sheet=book.Sheets[book.SheetNames[index]];
    range=sheet['!ref']?XLSX.utils.decode_range(sheet['!ref']):null;
    const csvHeader=ctx.type.ext==='csv' && range ? range.s.r : null;
    const full=sheet['!fullref']?XLSX.utils.decode_range(sheet['!fullref']):range;
    note.hidden=!full || (full.e.r<20000 && full.e.c<512);
    if (range) {
      range.e.r=Math.min(range.e.r,19999);
      range.e.c=Math.min(range.e.c,511);
      if (csvHeader!==null && range.e.r>range.s.r) range.s.r++;
      if (range.e.r<range.s.r || range.e.c<range.s.c) range=null;
    }
    batch=range?Math.min(160,Math.max(1,Math.floor(8000/(range.e.c-range.s.c+1)))):160;
    buttons.forEach((b,i)=>b.setAttribute('aria-selected',String(index===i)));
    previous.disabled=block===0;
    next.disabled=(block+1)*batch>=amount();
    labels();
    if (!range) {scroll.replaceChildren(badge('noRows'));translate(scroll);return;}
    const table=node('table','sheet-table'), header=table.createTHead().insertRow(), body=table.createTBody();
    const lineNumber=node('th');
    lineNumber.dataset.label='spreadsheetRow';
    header.append(lineNumber);
    for (let c=range.s.c;c<=range.e.c;c++) {
      const heading=csvHeader===null?null:sheet[XLSX.utils.encode_cell({r:csvHeader,c})];
      const th=node('th');
      th.scope='col';
      th.title=th.textContent=heading?.w || heading?.v || XLSX.utils.encode_col(c);
      header.append(th);
    }
    const end=Math.min(range.e.r+1,range.s.r+(block+1)*batch);
    for (let r=range.s.r+block*batch;r<end && r!==csvHeader;r++) {
      const row=body.insertRow();
      row.insertCell().textContent=String(r+1);
      for (let c=range.s.c;c<=range.e.c;c++) {
        const address=XLSX.utils.encode_cell({r,c});
        showCell(row.insertCell(),sheet[address],address,book.SheetNames[index]);
      }
    }
    scroll.replaceChildren(table);
    translate(scroll);
    scroll.scrollTop=0;
    scroll.scrollLeft=0;
  }
  ctx.host.replaceChildren(tools,note,...(canEdit?[editNote]:[]),scroll,...(buttons.length>1?[tabs]:[]));
  syncEdits();
  draw();
  return {language:labels,dirty,save,saving:()=>saving,destroy(){window.removeEventListener('beforeunload',beforeUnload);}};
}

async function pdfTask(url,signal,small=false) {
  const pdfjs=await import('/vendor/pdfjs-dist/build/pdf.mjs');
  signal.throwIfAborted();
  const base='/vendor/pdfjs-dist/';
  pdfjs.GlobalWorkerOptions.workerSrc=base+'build/pdf.worker.mjs';
  return pdfjs.getDocument({
    url,cMapUrl:base+'cmaps/',cMapPacked:true,standardFontDataUrl:base+'standard_fonts/',
    isEvalSupported:false,enableXfa:false,disableAutoFetch:small,disableStream:small
  });
}
async function pdf(ctx) {
  const task=await pdfTask(ctx.url,ctx.signal);
  let stopped=false;
  function stopTask() {
    if (!stopped) {stopped=true;task.destroy().catch(()=>{});}
  }
  ctx.signal.addEventListener('abort',stopTask,{once:true});
  try {
    const pdfDoc=await task.promise;
    waitLive(ctx);
    const first=await pdfDoc.getPage(1);
    waitLive(ctx);
    const tools=toolbar(), scroll=node('div','preview-scroll pdf-scroll'), field=node('input'), total=node('output'), zoomLabel=node('output');
    field.type='number';field.min='1';field.max=String(pdfDoc.numPages);field.value='1';field.dataset.label='page';
    total.textContent='/ '+pdfDoc.numPages;
    let scale=1,pageNumber=1,version=0,lastWidth=0;
    const all=[], live=new Set(), pending=new Map();
    const firstSize=first.getViewport({scale:1});
    const fitWidth=()=>Math.max(30,scroll.clientWidth-20);
    const setPage=(n)=>{
      pageNumber=clamp(Math.floor(n)||1,1,all.length);
      field.value=String(pageNumber);
      scroll.scrollTop=all[pageNumber-1].element.offsetTop;
    };
    field.onchange=()=>setPage(Number(field.value));
    field.onkeydown=(e)=>{if (e.key==='Enter') setPage(Number(field.value));};
    const label=()=>{zoomLabel.textContent=Math.round(scale*100)+'%';};
    const setScale=(n)=>{scale=clamp(n,.25,4);layout(true);};
    tools.append(ctx.button('previous','chevron',()=>setPage(pageNumber-1)),field,total,
      ctx.button('next','chevron',()=>setPage(pageNumber+1)),ctx.button('zoomOut','minus',()=>setScale(scale/1.24)),
      zoomLabel,ctx.button('zoomIn','plus',()=>setScale(scale*1.24)),ctx.button('width','fit',()=>setScale(1),'button',true));
    tools.firstChild.firstChild.style.transform='rotate(180deg)';
    const pages=node('div','pdf-pages');
    pages.style.position='relative';
    for (let i=0;i<pdfDoc.numPages;i++) {
      const element=node('div','pdf-page');
      element.textContent=String(i+1);
      element.dataset.index=String(i);
      all.push({element,width:firstSize.width,height:firstSize.height});
      pages.append(element);
    }
    scroll.append(pages);
    ctx.host.replaceChildren(tools,scroll);

    function dimensions(item) {
      const width=fitWidth()*scale;
      item.element.style.width=Math.round(width)+'px';
      item.element.style.height=Math.round(width*item.height/item.width)+'px';
      item.element.style.marginLeft=width>fitWidth()?'0':'auto';
    }
    function layout(force=false) {
      const width=fitWidth();
      if (width===lastWidth && !force) return;
      lastWidth=width;
      const anchor=all[pageNumber-1]?.element;
      const fraction=anchor?clamp((scroll.scrollTop-anchor.offsetTop)/anchor.offsetHeight,0,1):0;
      ++version;
      for (const render of pending.values()) render.cancel();
      pending.clear();
      for (const item of all) dimensions(item);
      if (anchor) scroll.scrollTop=anchor.offsetTop+anchor.offsetHeight*fraction;
      for (const index of live) render(index);
      label();
    }
    async function render(index) {
      const stamp=version;
      const item=all[index];
      if (item.rendered===stamp || item.requested===stamp) return;
      item.requested=stamp;
      try {
        const page=index===0?first:await pdfDoc.getPage(index+1);
        if (stopped || stamp!==version || !live.has(index)) return;
        const natural=page.getViewport({scale:1});
        item.width=natural.width;
        item.height=natural.height;
        dimensions(item);
        const theoretical=fitWidth()*scale/natural.width;
        const dpi=Math.min(devicePixelRatio||1,2,Math.sqrt(12000000/(natural.width*natural.height))/theoretical);
        const viewport=page.getViewport({scale:theoretical*dpi});
        const canvas=node('canvas');
        canvas.width=Math.ceil(viewport.width);
        canvas.height=Math.ceil(viewport.height);
        const request=page.render({canvasContext:canvas.getContext('2d',{alpha:false}),viewport});
        pending.set(index,request);
        try {await request.promise;} finally {if (pending.get(index)===request) pending.delete(index);}
        if (stopped || stamp!==version || !live.has(index)) return;
        item.element.replaceChildren(canvas);
        item.rendered=stamp;
      } catch(error) {
        if (!stopped && error?.name!=='RenderingCancelledException') {
          ctx.report('readError');
          stopTask();
        }
      } finally {if (item.requested===stamp) delete item.requested;}
    }
    const intersection=new IntersectionObserver((items)=>{
      for (const observation of items) {
        const index=Number(observation.target.dataset.index),item=all[index];
        if (observation.isIntersecting) {live.add(index);render(index);}
        else {
          live.delete(index);
          pending.get(index)?.cancel();
          item.element.textContent=String(index+1);
          delete item.rendered;
        }
      }
    },{root:scroll,rootMargin:'500px 0px'});
    const resize=new ResizeObserver(()=>layout());
    resize.observe(scroll);
    layout();
    for (const item of all) intersection.observe(item.element);
    scroll.onscroll=()=>{
      const offset=scroll.scrollTop+Math.min(32,scroll.clientHeight/4);
      let lo=0,hi=all.length-1;
      while (lo<hi) {
        const mid=Math.ceil((lo+hi)/2);
        if (all[mid].element.offsetTop<=offset) lo=mid;
        else hi=mid-1;
      }
      pageNumber=lo+1;
      if (document.activeElement!==field) field.value=String(pageNumber);
    };
    const cleanup=()=>{
      intersection.disconnect();resize.disconnect();
      for (const render of pending.values()) render.cancel();
      stopTask();
    };
    ctx.signal.addEventListener('abort',cleanup,{once:true});
    return {destroy:cleanup,language:label};
  } catch(e) {stopTask();throw e;}
}

function encodingSelect(ctx, detected) {
  const select=node('select','encoding-select');
  select.dataset.label='textEncoding';
  const options=[['auto',t('encodingAuto',{encoding:detected})],['utf-8','UTF-8'],['utf-16le','UTF-16 LE'],['utf-16be','UTF-16 BE'],['gb18030','GB18030']];
  for (const [value,label] of options) {
    const option=node('option');
    option.value=value;option.textContent=label;
    select.append(option);
  }
  select.value=ctx.encoding||'auto';
  select.onchange=()=>ctx.setEncoding(select.value);
  return select;
}

function encodingFailure(ctx) {
  waitLive(ctx);
  const tools=toolbar();
  tools.append(encodingSelect(ctx,'—'));
  ctx.host.replaceChildren(tools,badge('encoding'));
}
function decodeCSV(buffer,preferred) {
  const bytes=new Uint8Array(buffer);
  const candidates=preferred && preferred!=='auto'?[preferred]:
    bytes[0]===255 && bytes[1]===254?['utf-16le']:bytes[0]===254 && bytes[1]===255?['utf-16be']:['utf-8','gb18030'];
  for (const encoding of candidates) {
    try {
      const text=new TextDecoder(encoding,{fatal:true}).decode(buffer);
      if (!text.includes('\u0000')) return {text,encoding:encoding.toUpperCase()};
    } catch {}
  }
  throw {error:'encoding'};
}

function audioPreview(ctx) {
  const view=node('div','audio-preview'), card=node('div','audio-card'), symbol=node('div','audio-symbol');
  symbol.innerHTML=icon('audio');
  const name=node('strong');
  name.textContent=ctx.entry.name;
  const player=node('audio');
  player.controls=true;
  player.preload='auto';
  const speed=node('select','encoding-select');
  speed.dataset.label='playbackSpeed';
  for (const rate of [.5,.75,1,1.25,1.5,2]) {
    const option=node('option');
    option.value=String(rate);option.textContent=rate+'×';
    speed.append(option);
  }
  speed.value='1';
  speed.onchange=()=>{player.playbackRate=Number(speed.value);};
  const feedback=node('p','notice');
  feedback.hidden=true;
  let retried=false;
  player.onerror=async()=>{
    if (ctx.signal.aborted) return;
    if (retried) {ctx.report('audioPlaybackError');return;}
    retried=true;
    feedback.hidden=false;
    feedback.textContent=t('convertingAudio');
    try {
      player.src=await ctx.convertVideo();
      waitLive(ctx);
      feedback.hidden=true;
    } catch(error) {
      if (!ctx.signal.aborted) {
        const messages={videoToolMissing:'audioToolMissing',videoBusy:'audioConvertBusy',videoConvertFailed:'audioConvertFailed',videoLimit:'audioConvertLimit'};
        ctx.report(messages[error?.error] || error?.error || 'audioPlaybackError');
      }
    }
  };
  card.append(symbol,name,player,speed,feedback);
  view.append(card);
  ctx.host.replaceChildren(view);
  player.src=ctx.url;
  return {destroy(){player.onerror=null;player.pause();player.removeAttribute('src');player.load();}};
}

async function archiveView(ctx) {
  const archive=await ctx.archive();
  waitLive(ctx);
  const scroll=node('section','preview-scroll archive-scroll'), list=node('table','archive-table');
  const heading=node('thead'), row=node('tr');
  for (const key of ['name','archiveEntrySize','download']) {
    const cell=node('th');
    cell.dataset.i18n=key;
    row.append(cell);
  }
  heading.append(row);
  list.append(heading);
  const body=node('tbody');
  for (const member of archive.entries) {
    const row=node('tr');
    const label=node('td'), size=node('td'), operation=node('td');
    const name=node('span');
    name.textContent=name.title=member.name;
    label.innerHTML=icon(member.dir?'folder':'file');
    label.append(name);
    size.textContent=member.dir||member.size<0?'—':ctx.size(member.size);
    if (Number.isInteger(member.download)) {
      const link=node('a','icon-button');
      link.href=ctx.archiveMember(member.download);
      link.download='';
      link.dataset.label='download';
      link.innerHTML=icon('download');
      operation.append(link);
    }
    row.append(label,size,operation);
    body.append(row);
  }
  list.append(body);
  if (archive.limited) scroll.append(badge('archivePreviewPartial'));
  if (archive.entries.length) scroll.append(list);
  else scroll.append(badge('archiveEmpty'));
  ctx.host.replaceChildren(scroll);
}

function slidePage(data,imageURL) {
  const page=node('div','slide-page');
  page.style.aspectRatio=clamp(data.ratio,.4,4);
  for (const item of data.items) {
    const itemBox=node('div','slide-item');
    Object.assign(itemBox.style,{left:100*clamp(item.x,-1,2)+'%',top:100*clamp(item.y,-1,2)+'%',
      width:100*clamp(item.width,0,2)+'%',height:100*clamp(item.height,0,2)+'%'});
    if (item.image) {
      const image=node('img');
      image.alt='';image.src=imageURL(item.image);image.draggable=false;
      itemBox.append(image);
    }
    for (const value of item.text||[]) {
      const paragraph=node('p');
      paragraph.textContent=value;
      itemBox.append(paragraph);
    }
    page.append(itemBox);
  }
  return page;
}

async function presentationView(ctx) {
  let slide=await ctx.slide(1);
  waitLive(ctx);
  const tools=toolbar(), stage=node('div','preview-scroll slide-scroll');
  const position=node('input','slide-position'), count=node('output');
  const previous=ctx.button('previous','chevron',()=>show(slide.page-1));
  previous.firstChild.style.transform='rotate(180deg)';
  const next=ctx.button('next','chevron',()=>show(slide.page+1));
  position.type='number';position.min='1';position.dataset.label='page';
  position.onchange=()=>show(Number(position.value));
  const detail=badge('presentationLayout'), thumbnails=node('nav','slide-thumbnails');
  thumbnails.dataset.label='slideNavigation';
  const previews=new Map([[slide.page,Promise.resolve(slide)]]);
  tools.append(previous,position,count,next);
  ctx.host.replaceChildren(tools,stage,thumbnails,detail);
  let pending;
  function render() {
    position.value=String(slide.page);position.max=String(slide.count);
    previous.disabled=slide.page<=1;next.disabled=slide.page>=slide.count;
    count.textContent='/ '+slide.count;
    stage.replaceChildren(slidePage(slide,ctx.slideImage));
    const start=Math.max(1,Math.min(slide.page-2,slide.count-4));
    thumbnails.replaceChildren();
    for (let number=start;number<=Math.min(slide.count,start+4);number++) {
      const button=node('button','slide-thumb');
      button.type='button';
      button.setAttribute('aria-label',t('slideNumber',{number}));
      if (number===slide.page) button.setAttribute('aria-current','page');
      const preview=node('span','slide-thumb-preview'), label=node('span');
      label.textContent=String(number);
      button.append(preview,label);
      button.onclick=()=>show(number);
      thumbnails.append(button);
      if (!previews.has(number)) {
        const loading=ctx.slide(number).catch(error=>{previews.delete(number);throw error;});
        previews.set(number,loading);
        if (previews.size>24) previews.delete(previews.keys().next().value);
      }
      previews.get(number).then(page=>{
        if (button.isConnected && !ctx.signal.aborted) preview.append(slidePage(page,ctx.slideImage));
      }).catch(()=>{});
    }
  }
  async function show(page) {
    if (!Number.isInteger(page) || page<1 || page>slide.count) {render();return;}
    pending?.abort();
    pending=new AbortController();
    const {signal}=pending;
    try {const selected=await (previews.get(page) || ctx.slide(page,signal));if (!signal.aborted) {slide=selected;render();}}
    catch(error) {if (!signal.aborted) ctx.notify(error);}
  }
  render();
  ctx.signal.addEventListener('abort',()=>pending?.abort(),{once:true});
  return {destroy(){pending?.abort();}};
}

function mediaEvent(media,event,signal,timeout=6000) {
  return new Promise((resolve,reject)=>{
    function cleanup() {
      clearTimeout(timer);
      media.removeEventListener(event,ready);
      media.removeEventListener('error',fail);
      signal.removeEventListener('abort',fail);
    }
    function ready(){cleanup();resolve();}
    function fail(){cleanup();reject(new Error('Media unavailable'));}
    const timer=setTimeout(fail,timeout);
    media.addEventListener(event,ready,{once:true});
    media.addEventListener('error',fail,{once:true});
    signal.addEventListener('abort',fail,{once:true});
    if (signal.aborted) fail();
  });
}

function tileImage(url) {
  const image=node('img','grid-preview-content');
  image.loading='lazy';image.decoding='async';image.alt='';image.draggable=false;
  image.onerror=()=>image.remove();
  image.src=url;
  return image;
}

function tileLines(lines,style='') {
  const paper=node('div','grid-preview-content tile-paper '+style);
  for (const line of lines.slice(0,6)) {
    const row=node('div');
    row.textContent=String(line || ' ').slice(0,100);
    paper.append(row);
  }
  return lines.some(Boolean)?paper:null;
}

async function videoTile(ctx) {
  const video=node('video');
  video.preload='auto';video.muted=true;video.playsInline=true;
  try {
    const loaded=mediaEvent(video,'loadeddata',ctx.signal);
    video.src=ctx.url;
    await loaded;
    if (Number.isFinite(video.duration) && video.duration>1) {
      const seek=mediaEvent(video,'seeked',ctx.signal,2500);
      video.currentTime=Math.min(1,video.duration*.1);
      await seek;
    }
    if (!video.videoWidth || !video.videoHeight) throw new Error('Empty frame');
    const canvas=node('canvas','grid-preview-content');
    const scale=240/Math.max(video.videoWidth,video.videoHeight);
    canvas.width=Math.max(1,Math.round(video.videoWidth*scale));
    canvas.height=Math.max(1,Math.round(video.videoHeight*scale));
    canvas.getContext('2d').drawImage(video,0,0,canvas.width,canvas.height);
    return canvas;
  } catch(error) {
    if (ctx.signal.aborted) return null;
    const poster=tileImage(ctx.api('video-poster'));
    poster.loading='eager';
    const loaded=mediaEvent(poster,'load',ctx.signal,16000);
    if (!poster.complete || !poster.naturalWidth) await loaded;
    else loaded.catch(()=>{});
    return poster;
  } finally {
    video.removeAttribute('src');video.load();
  }
}

async function audioTile(ctx) {
  try {
    const audio=node('audio');
    audio.preload='metadata';
    const loaded=mediaEvent(audio,'loadedmetadata',ctx.signal);
    audio.src=ctx.url;
    let duration;
    try {await loaded;duration=audio.duration;}
    finally {audio.removeAttribute('src');audio.load();}
    if (ctx.entry.size<=2*1024*1024 && typeof OfflineAudioContext!=='undefined' && duration>0 && duration<=180) {
      const bytes=await (await ctx.read()).arrayBuffer();
      const decoded=await new OfflineAudioContext(1,1,44100).decodeAudioData(bytes);
      const samples=decoded.getChannelData(0), canvas=node('canvas','grid-preview-content audio-waveform');
      canvas.width=240;canvas.height=110;
      const paint=canvas.getContext('2d');
      paint.fillStyle='#458ade';
      for (let i=0;i<60;i++) {
        const start=Math.floor(samples.length*i/60), end=Math.floor(samples.length*(i+1)/60);
        let peak=0;
        for (let j=start;j<end;j+=Math.max(1,Math.floor((end-start)/100))) peak=Math.max(peak,Math.abs(samples[j]));
        const height=Math.max(4,100*peak);
        paint.fillRect(i*4,(110-height)/2,3,height);
      }
      return canvas;
    }
    if (Number.isFinite(duration) && duration>0) return tileLines([ctx.type.ext.toUpperCase(),Math.floor(duration/60)+':'+String(Math.floor(duration%60)).padStart(2,'0')],'tile-audio');
  } catch {}
  return tileLines([ctx.type.ext.toUpperCase(),new Intl.NumberFormat(undefined,{unit:'kilobyte',style:'unit',maximumFractionDigits:0}).format(ctx.entry.size/1024)],'tile-audio');
}

async function gridThumbnail(ctx) {
  const {ext,style}=ctx.type;
  switch(style) {
    case 'image':
      if (raster.has(ext)) return tileImage(ctx.api('raster',{thumbnail:'1'}));
      if (heif.has(ext)) {
        const result=await heifImage(ctx);
        const canvas=node('canvas','grid-preview-content');
        const original=result.canvas;
        const scale=Math.min(1,240/Math.max(original.width,original.height));
        canvas.width=Math.max(1,Math.round(original.width*scale));
        canvas.height=Math.max(1,Math.round(original.height*scale));
        canvas.getContext('2d').drawImage(original,0,0,canvas.width,canvas.height);
        original.width=original.height=0;
        return canvas;
      }
      if (['jpg','jpeg','png'].includes(ext) && ctx.entry.size<=96*1024*1024) {
        const image=tileImage(ctx.api('thumbnail',{edge:240}));
        image.onerror=()=>{
          image.onerror=()=>image.remove();
          if (ctx.entry.size<=12*1024*1024) image.src=ctx.url;
          else image.remove();
        };
        return image;
      }
      return tileImage(ctx.url);
    case 'video': return videoTile(ctx);
    case 'audio': return audioTile(ctx);
    case 'text': case 'code': {
      try {
        const text=await ctx.json('text',{thumbnail:1});
        return tileLines(text.text.split(/\r?\n/),'tile-code');
      } catch(error) {
        if (ext==='ts' && !ctx.signal.aborted) return videoTile(ctx);
        throw error;
      }
    }
    case 'archive': {
      const archive=await ctx.json('archive',{thumbnail:1});
      return tileLines(archive.entries.map(item=>(item.dir?'▸ ':'')+item.name));
    }
    case 'doc': {
      if (ctx.entry.size>12<<20) return null;
      const doc=await ctx.json('word',{thumbnail:1});
      return tileLines(doc.paragraphs);
    }
    case 'sheet': {
      if (ctx.entry.size>12<<20) return null;
      const bytes=await arrayBuffer(ctx,12<<20);
      await script('/vendor/xlsx/xlsx.full.min.js');
      const data=ext==='csv'?decodeCSV(bytes).text:bytes;
      if (ext==='xlsx') verifyOfficeZip(bytes);
      const XLSX=window.XLSX;
      const book=XLSX.read(data,{type:typeof data==='string'?'string':'array',sheetRows:6,sheets:0});
      const sheet=book.Sheets[book.SheetNames[0]];
      if (!sheet) return null;
      const table=node('table','grid-preview-content tile-table');
      for (const row of XLSX.utils.sheet_to_json(sheet,{header:1,raw:false}).slice(0,5)) {
        const tr=node('tr');
        for (const value of row.slice(0,4)) {
          const cell=node('td');cell.textContent=value;
          tr.append(cell);
        }
        table.append(tr);
      }
      return table.rows.length?table:null;
    }
    case 'pdf': {
      const task=await pdfTask(ctx.url,ctx.signal,true);
      const cancel=()=>task.destroy().catch(()=>{});
      ctx.signal.addEventListener('abort',cancel,{once:true});
      try {
        const pdf=await task.promise, page=await pdf.getPage(1);
        const size=page.getViewport({scale:1});
        const viewport=page.getViewport({scale:200/Math.max(size.height,size.width)});
        const canvas=node('canvas','grid-preview-content');
        canvas.width=Math.ceil(viewport.width);canvas.height=Math.ceil(viewport.height);
        await page.render({canvasContext:canvas.getContext('2d'),viewport}).promise;
        return canvas;
      } finally {
        ctx.signal.removeEventListener('abort',cancel);
        await cancel();
      }
    }
    case 'slides': {
      const data=await ctx.json('presentation',{page:1});
      const slide=slidePage(data,part=>ctx.api('presentation-image',{part}));
      slide.classList.add('grid-preview-content');
      return slide;
    }
    default: return null;
  }
}

export function mountGridThumbnails(grid,items,signal,route,fileURL,cache) {
  const queue=[], pending=new Map(items.map(item=>[item.element,item]));
  let running=0;
  const rendered=new Set();
  const observer=new IntersectionObserver(records=>{
    for (const record of records) {
      if (!record.isIntersecting) continue;
      observer.unobserve(record.target);
      queue.push(pending.get(record.target));
      pending.delete(record.target);
    }
    pump();
  },{root:grid,rootMargin:'100px'});
  signal.addEventListener('abort',()=>{
    observer.disconnect();queue.length=0;pending.clear();
    for (const element of rendered) element.remove();
    rendered.clear();
  },{once:true});
  for (const item of items) observer.observe(item.element);
  function pump() {
    while (!signal.aborted && running<3 && queue.length) {
      running++;
      render(queue.shift()).catch(()=>{}).finally(()=>{running--;pump();});
    }
  }
  async function render({element,entry,type}) {
    const key=entry.path+'\0'+entry.version;
    function attach(content) {
      rendered.add(content);
      element.append(content);
    }
    const previous=cache.get(key);
    if (previous) {
      cache.delete(key);
      cache.set(key,previous);
      attach(previous);
      return;
    }
    const api=(action,options)=>route(action,entry.path,options);
    const url=fileURL(entry.path);
    const response=async address=>{
      const result=await fetch(address,{signal});
      if (!result.ok) throw new Error('Preview unavailable');
      return result;
    };
    const content=await gridThumbnail({entry,type,api,url,signal,read:()=>response(url),json:async(action,options)=>(await response(api(action,options))).json()});
    if (!content || signal.aborted) return;
    attach(content);
    function remember() {
      if (!signal.aborted && (content instanceof HTMLImageElement
        ? content.naturalWidth*content.naturalHeight<=1_000_000
        : !content.querySelector('img'))) {
        cache.set(key,content);
        if (cache.size>96) cache.delete(cache.keys().next().value);
      }
    }
    if (content instanceof HTMLImageElement && !content.naturalWidth) {
      content.addEventListener('load',()=>{if (content.naturalWidth) remember();});
    } else remember();
  }
}
