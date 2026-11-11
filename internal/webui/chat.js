'use strict';
const chatState={snapshot:null,records:[],controller:null,report:null,status:null};

function chatNotice(text,error=false) {
  $('chat-status').textContent=text;$('chat-status').classList.toggle('error',error);
}
function chatContext() {
  const focusPath=[];let parent=mode.parentReview;
  while(parent) {if(parent.flowSymbolID)focusPath.unshift(parent.flowSymbolID);parent=parent.parentReview;}
  return {module:report?.module,kind:mode.kind,entryID:mode.kind==='endpoint'?mode.id:undefined,
    focusSymbolID:mode.flowSymbolID,focusPath,expandedIDs:mode.expandedActivities||[],
    selected:mode.selected,documentID:mode.documentID,description:$('breadcrumb')?.textContent};
}
function chatFingerprint() {return JSON.stringify(chatContext());}
function chatSave() {
  if(!chatState.snapshot)return;
  try {sessionStorage.setItem('go-review-chat:'+chatState.snapshot.id,JSON.stringify(chatState.records.slice(-40)));}
  catch(error) {chatNotice('Could not retain chat history: '+error.message,true);}
}
function chatCitations(parent,text,snapshotID) {
  const seen=new Set();
  for(const match of text.matchAll(/\[(document|symbol|entry):([^\]\n]+)\]/g)) {
    if(seen.has(match[0]))continue;seen.add(match[0]);
    const action={view:match[1]==='entry'?'flow':match[1],id:match[2],
      entryID:match[1]==='entry'?match[2]:undefined,snapshotID};
    const link=button('Evidence · '+match[1],()=>chatNavigate(action),parent);
    link.className='chat-citation';link.title=match[2];
  }
}
function chatRender(item) {
  if(item.record.role!=='assistant') {
    item.content.textContent=item.record.content;return;
  }
  GoReviewMarkdown.render(item.content,item.record.content);
  for(const link of item.content.querySelectorAll('a[href]'))
    if(!/^https?:\/\/|^mailto:/i.test(link.getAttribute('href')))link.removeAttribute('href');
}
function chatRecord(role,text) {
  const record={role,content:text};chatState.records.push(record);
  const article=el('article',undefined,$('chat-messages'),'chat-message chat-'+role);
  el('strong',role==='user'?'You':'Local review agent · assessment, not measured evidence',article);
  const content=el('div',undefined,article,'chat-text');
  const item={record,article,content};chatRender(item);
  return item;
}
function chatScroll() {$('chat-messages').scrollTop=$('chat-messages').scrollHeight;}
async function chatLoad() {
  const response=await fetch('/api/chat/status');
  const data=await response.json();
  if(!response.ok)throw Error(data.error||'Unable to load agent status');
  chatState.status=data;
  $('chat-model').textContent=data.enabled?data.model+' · '+data.baseURL:'AI disabled';
  if(chatState.snapshot?.id!==data.snapshot?.id) {
    chatState.controller?.abort();chatState.lastNavigation=undefined;chatState.records=[];$('chat-messages').replaceChildren();
    chatState.snapshot=data.snapshot;chatState.report=report;
    if(data.snapshot) {
      const saved=sessionStorage.getItem('go-review-chat:'+data.snapshot.id);
      if(saved) {
        const records=JSON.parse(saved);
        if(!Array.isArray(records)||records.some(r=>!['user','assistant'].includes(r.role)||typeof r.content!=='string'))
          throw Error('Invalid saved chat history; use Clear chat.');
        for(const r of records.slice(-40)) {const item=chatRecord(r.role,r.content);chatCitations(item.article,r.content,data.snapshot.id);}
      }
    }
  }
  $('chat-send').disabled=!data.enabled||!data.snapshot;
  chatNotice(!data.enabled?'Local AI is optional. Configure the machine-local go-review/agent.json to enable it.':
    !data.snapshot?'Open retained evidence first.':'Evidence stays local. No code execution. Navigation requires your permission.');
  return data;
}

// Only this bridge changes application state. Both server and browser validate
// retained IDs; generated text is never interpreted as an executable action.
async function chatNavigate(action,expectedFingerprint,signal) {
  try {
    const response=await fetch('/api/chat/status',{signal}),current=await response.json();
    if(!response.ok)throw Error(current.error||'Unable to verify current snapshot');
    if(chatState.report!==report||current.snapshot?.id!==chatState.snapshot?.id)
      throw Error('Snapshot changed; refresh the conversation before navigating.');
    signal?.throwIfAborted();
    if(expectedFingerprint!==undefined&&chatFingerprint()!==expectedFingerprint)
      throw Error('View changed manually; automatic navigation cancelled.');
    GoReviewChat.validate(report,action,current.snapshot.id);
    if(action.view==='application')go({kind:'application'});
    else if(action.view==='back') {if(!history.length)throw Error('No parent view in navigation history');$('back').click();}
    else if(action.view==='packages')go({kind:'packages'});
    else if(action.view==='package')go({kind:'package',id:action.id});
    else if(action.view==='symbol')go({kind:'logical',id:action.id});
    else if(action.view==='document')openDocumentation([...(report.documents||[]),...(report.entryPoints?.sources||[])].find(d=>d.id===action.id));
    else if(action.view==='architecture')go({kind:'system-flow',level:action.id});
    else if(action.view==='flow') {
      const historyLength=history.length;
      const entry=GoReviewQuery.scene(report,{entryID:action.entryID}).entry;
      go({kind:'endpoint',id:action.entryID});
      for(const symbolID of [...(action.focusPath||[]),...(action.focusSymbolID?[action.focusSymbolID]:[])]) {
        const raw=GoReviewQuery.rawScene(report,entry,mode.flowSymbolID);
        const tree=GoReviewVisual.hierarchy(report,raw);
        const node=[...tree.byID.values()].find(n=>n.symbolID===symbolID);
        if(!node)throw Error('Focus symbol is outside the visible flow');
        visualFocus(node,tree);
      }
      if(action.expandedIDs?.length)go({...mode,expandedActivities:action.expandedIDs});
      if(action.nodeID) {
        const node=overallEndpointScene.nodes.find(n=>n.id===action.nodeID);
        if(!node)throw Error('Selected flow occurrence is not visible');
        visualSelect(entry,overallEndpointScene,node);
        [...document.querySelectorAll('.review-node')].find(n=>n.dataset.nodeId===node.id)?.scrollIntoView?.({block:'nearest',inline:'nearest'});
      } else if(action.from) {
        const edge=overallEndpointScene.edges.find(e=>e.from===action.from&&e.to===action.to&&e.kind===(action.kind||'control'));
        if(!edge)throw Error('Selected edge is not visible');
        visualEdgeDetail(entry,overallEndpointScene,edge);
      }
      // Reconstructing focus/expansion is one user action, not several Back steps.
      history.splice(historyLength+1);
    }
    chatNotice('Showing '+(action.label||action.view)+'.');
    chatState.lastNavigation=chatContext();
    return true;
  } catch(error) {
    if(expectedFingerprint!==undefined)throw error;
    chatNotice('Navigation failed: '+error.message,true);return false;
  }
}

$('workspace-chat').onclick=async()=>{
  const open=$('chat-panel').hidden;
  $('chat-panel').hidden=!open;document.body.classList.toggle('chat-open',open);
  $('workspace-chat').setAttribute('aria-expanded',String(open));
  if(open)try{await chatLoad();$('chat-input').focus();}catch(error){chatNotice(error.message,true);}
};
$('chat-close').onclick=()=>{$('chat-panel').hidden=true;document.body.classList.remove('chat-open');$('workspace-chat').setAttribute('aria-expanded','false');};
$('chat-clear').onclick=()=>{
  chatState.controller?.abort();
  chatState.lastNavigation=undefined;chatState.records=[];$('chat-messages').replaceChildren();chatSave();chatNotice('Chat cleared.');
};
$('chat-stop').onclick=()=>chatState.controller?.abort();
$('chat-form').onsubmit=async event=>{
  event.preventDefault();
  if(chatState.controller)return;
  const text=$('chat-input').value.trim();if(!text)return;
  let assistant,completed=false,reader;
  try {
    const state=await chatLoad();
    if(!state.enabled||!state.snapshot)throw Error('Local agent or evidence is unavailable');
    const prior=chatState.records.slice(-38).map(({role,content})=>({role,content}));
    chatRecord('user',text);assistant=chatRecord('assistant','');
    const allowNavigation=$('chat-allow-navigation').checked;
    $('chat-allow-navigation').checked=false;
    let fingerprint=chatFingerprint();
    const context=chatContext();
    if(chatState.lastNavigation)context.lastConfirmedNavigation=chatState.lastNavigation;
    const controller=new AbortController();chatState.controller=controller;
    $('chat-send').disabled=true;$('chat-stop').disabled=false;$('chat-input').value='';
    chatNotice('Exploring retained evidence…');
    const response=await fetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json'},
      signal:controller.signal,body:JSON.stringify({message:text,snapshotID:state.snapshot.id,history:prior,
        context,allowNavigation})});
    if(!response.ok){const error=await response.json();throw Error(error.error||'Chat HTTP '+response.status);}
    reader=response.body.getReader();
    const decoder=new TextDecoder();let pending='',turnStart=0,navigated=false;
    while(true) {
      const part=await reader.read();
      pending+=decoder.decode(part.value||new Uint8Array(),{stream:!part.done});
      let end;
      while((end=pending.indexOf('\n'))>=0) {
        const line=pending.slice(0,end);pending=pending.slice(end+1);if(!line.trim())continue;
        const message=JSON.parse(line);
        if(message.type==='turn_start')turnStart=assistant.record.content.length;
        else if(message.type==='discard_turn') {
          assistant.record.content=assistant.record.content.slice(0,turnStart);chatRender(assistant);
        }
        else if(message.type==='text') {assistant.record.content+=message.text;chatRender(assistant);}
        else if(message.type==='tool'||message.type==='tool_error') {
          const item=el('details',undefined,assistant.article,'chat-tool');
          el('summary',(message.type==='tool_error'?'Tool error · ':'Evidence · ')+message.tool,item);
          el('pre',message.type==='tool_error'?message.text:JSON.stringify(message.args,null,2),item);
          if(message.type==='tool') {
            const args=message.args||{};
            if(message.tool==='review_document'&&args.id)chatCitations(item,'[document:'+args.id+']',state.snapshot.id);
            if(args.symbolID)chatCitations(item,'[symbol:'+args.symbolID+']',state.snapshot.id);
            if(args.entryID)chatCitations(item,'[entry:'+args.entryID+']',state.snapshot.id);
          }
        } else if(message.type==='navigation') {
          const action=message.navigation;
          if(allowNavigation&&action.automatic) {
            await chatNavigate(action,fingerprint,controller.signal);
            navigated=true;
            fingerprint=chatFingerprint();
          } else {
            const show=button('Show in diagram · '+(action.label||action.view),()=>chatNavigate(action),assistant.article);
            show.className='chat-navigation';
          }
        } else if(message.type==='error')throw Error(message.text);
        else if(message.type==='done')completed=true;
        chatScroll();
      }
      if(part.done)break;
    }
    if(!completed)throw Error('Chat stream ended before completion.');
    chatCitations(assistant.article,assistant.record.content,state.snapshot.id);
    chatNotice('Response complete · '+(navigated?'UI navigation applied.':'No automatic UI navigation performed.')+' Model assessment, not certification.');
  }catch(error){
    const reason=error.name==='AbortError'?'Stopped.':error.message;
    if(assistant)el('p',reason,assistant.article,'error');
    chatNotice(reason,error.name!=='AbortError');
    if(assistant&&!completed) {assistant.record.content+='\n[Response incomplete: '+reason+']';}
  }finally {
    chatState.controller?.abort();
    if(reader)reader.releaseLock();
    chatState.controller=null;$('chat-send').disabled=!chatState.status?.enabled;$('chat-stop').disabled=true;chatSave();
  }
};

const beforeChatRender=render;
render=function(){
  beforeChatRender();
  if(chatState.report&&chatState.report!==report) {
    chatState.controller?.abort();
    chatNotice('Evidence snapshot changed. Start a new turn to load its conversation.');
  }
};
