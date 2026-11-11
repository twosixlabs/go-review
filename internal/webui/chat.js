'use strict';
const chatState={snapshot:null,records:[],controller:null,report:null,status:null,sending:false};

function chatNotice(text,error=false) {
  $('chat-status').textContent=text;$('chat-status').classList.toggle('error',error);
}
function chatContext() {
  if(typeof changeState!=='undefined'&&changeState.active&&changeState.id) {
    if(changeState.detailInput)return {kind:'changes',comparisonID:changeState.id,operation:'side',...changeState.detailInput};
    const flow=changeState.flow,record=changeState.record;
    const source=changeState.fileID&&!$('change-reader').hidden;
    return {kind:'changes',comparisonID:changeState.id,operation:source?'source':changeState.inventoryFile?'files':record?'record':flow?'flow':changeState.perspective==='files'?'files':'overview',fileID:source||changeState.inventoryFile?changeState.fileID:undefined,
      recordID:record?.id,flowID:flow?.id,...(flow?changeSideSnapshot(flow):{}),side:changeState.side,
      perspective:changeState.perspective==='flows'||changeState.perspective==='files'?'all':changeState.perspective,
      status:changeState.status,focusPath:changeState.focusPath,focusSymbolID:changeState.focusSymbolID,expandedIDs:changeState.expanded,
      description:$('change-heading').textContent};
  }
  const focusPath=[];let parent=mode.parentReview;
  while(parent) {if(parent.flowSymbolID)focusPath.unshift(parent.flowSymbolID);parent=parent.parentReview;}
  const callable=['logical','symbol','dataflow','review'].includes(mode.kind);
  return {module:report?.module,kind:mode.kind,entryID:mode.kind==='endpoint'?mode.id:undefined,
    focusSymbolID:mode.flowSymbolID,focusPath,expandedIDs:mode.expandedActivities||[],
    selected:mode.selected,documentID:mode.documentOpen?mode.documentID:undefined,description:$('breadcrumb')?.textContent,
    symbolID:callable?mode.id:undefined,control:mode.kind==='symbol'&&typeof preferControl!=='undefined'?preferControl:undefined,
    package:mode.kind==='package'?mode.id:mode.package,section:mode.section,
    findingID:mode.kind==='signals'?mode.id:undefined,checkID:mode.checkID,
    workspacePanel:mode.workspacePanel,stepID:mode.reviewStepID,
    query:mode.query,minComplexity:mode.minComplexity,maxCoverage:mode.maxCoverage,coverage:mode.coverage,cohort:mode.cohort,
    valueID:mode.value,direction:mode.direction,depth:Number.isFinite(mode.depth)?mode.depth:undefined,
    level:mode.level,componentID:mode.componentID,operationID:mode.operationID,group:mode.group};
}
function chatFingerprint() {return JSON.stringify(chatContext());}
function chatSave() {
  if(!chatState.snapshot)return;
  try {sessionStorage.setItem('go-review-chat:'+chatState.snapshot.id,JSON.stringify(chatState.records));}
  catch(error) {chatNotice('Could not retain chat history: '+error.message,true);}
}
function chatCitations(parent,text,snapshotID) {
  const seen=new Set();
  for(const match of text.matchAll(/\[(change|change-source):([^\]\n]+)\]/g)) {
    if(seen.has(match[0]))continue;seen.add(match[0]);
    const [comparisonID,id,side]=match[2].split('|');
    const input=match[1]==='change'?{comparisonID,operation:'record',recordID:id}:{comparisonID,operation:'source',fileID:id,side};
    button('Evidence · '+(side?side+' source':'change'),()=>changeAct(()=>changeNavigate(input)),parent).className='chat-citation';
  }
  for(const match of text.matchAll(/\[(document|symbol|entry):([^\]\n]+)\]/g)) {
    if(seen.has(match[0]))continue;seen.add(match[0]);
    const [id,evidenceSnapshot]=match[2].split('|');
    const action={view:match[1]==='entry'?'flow':match[1],id,
      entryID:match[1]==='entry'?id:undefined,snapshotID:evidenceSnapshot||snapshotID};
    const link=button('Evidence · '+match[1],()=>chatNavigate(action),parent);
    link.className='chat-citation';link.title=match[2];
  }
}
function chatRender(item) {
  if(item.record.role!=='assistant') {
    item.content.textContent=item.record.content;return;
  }
  chatMarkdown(item.content,item.record.content);
}
function chatMarkdown(target,text) {
  GoReviewMarkdown.render(target,text);
  for(const link of target.querySelectorAll('a[href]'))
    if(!/^https?:\/\/|^mailto:/i.test(link.getAttribute('href')))link.removeAttribute('href');
}
function chatRecord(role,text) {
  const record={role,content:text,snapshotID:chatState.snapshot?.id};chatState.records.push(record);
  const article=el('article',undefined,$('chat-messages'),'chat-message chat-'+role);
  el('strong',role==='user'?'You':'Review assistant',article);
  let activity,activitySummary,activityBody;
  if(role==='assistant') {
    activity=el('details',undefined,article,'chat-activity');
    activity.hidden=true;
    activitySummary=el('summary','Working…',activity);
    activityBody=el('div',undefined,activity,'chat-activity-body');
  }
  const content=el('div',undefined,article,'chat-text');
  const item={record,article,content,activity,activitySummary,activityBody};chatRender(item);
  return item;
}
const chatToolLabels={review_overview:'Reading application overview',review_flow:'Tracing application flow',
  review_inspect:'Inspecting a responsibility',review_source:'Reading source',review_document:'Reading documentation',
  review_evidence:'Inspecting implementation evidence',review_packages:'Exploring packages',
  review_search:'Searching retained evidence',review_architecture:'Examining architecture',
  review_assess:'Examining review priorities and signals',review_dataflow:'Tracing retained value dependencies',
  review_results:'Reading repository review results',
  review_changes:'Reading pinned Change Review evidence',navigate_changes:'Preparing Change Review navigation',
  propose_change_action:'Preparing a confirmation request',
  recall_evidence:'Recalling earlier evidence',navigate_ui:'Preparing navigation'};
function chatWorking(item,text) {
  item.activity.hidden=false;item.activity.classList.add('working');
  item.activitySummary.textContent=text;item.article.setAttribute('aria-busy','true');
  chatNotice(text);
}
function chatExploration(item) {
  if(!item.record.content.trim())return;
  chatActivity(item,{text:item.record.content});
  item.record.content='';chatRender(item);
}
function chatActivity(item,entry,retain=true) {
  if(retain)(item.record.activities??=[]).push(entry);
  item.activity.hidden=false;
  if(!entry.tool) {
    const progress=el('div',undefined,item.activityBody,'chat-text chat-progress');
    chatMarkdown(progress,entry.text);
    return;
  }
  const detail=el('details',undefined,item.activityBody,'chat-tool');
  el('summary',entry.error?'Could not complete '+entry.tool:(chatToolLabels[entry.tool]||entry.tool),detail);
  el('pre',entry.error?entry.text:JSON.stringify(entry.args,null,2),detail);
  if(!entry.error) {
    const args=entry.args||{},snapshotID=entry.snapshotID;
    if(entry.tool==='review_document'&&args.id)chatCitations(detail,'[document:'+args.id+']',snapshotID);
    if(args.symbolID)chatCitations(detail,'[symbol:'+args.symbolID+']',snapshotID);
    if(args.entryID)chatCitations(detail,'[entry:'+args.entryID+']',snapshotID);
  }
}
function chatScroll() {$('chat-messages').scrollTop=$('chat-messages').scrollHeight;}
async function chatLoad() {
  const response=await fetch('/api/chat/status');
  const data=await response.json();
  if(!response.ok)throw Error(data.error||'Unable to load agent status');
  if(typeof changeState!=='undefined'&&changeState.active&&changeState.id) {
    if(data.comparison?.id!==changeState.id)throw Error('Comparison changed; reload Change Review before continuing.');
    data.snapshot={id:'changes:'+data.comparison.id,module:'Change Review',comparisonID:data.comparison.id};
  }
  chatState.status=data;
  $('chat-model').textContent=data.enabled?data.model+' · '+data.baseURL:'AI disabled';
  $('chat-privacy').textContent=data.external?
    'External provider. Your messages and requested source evidence are sent to this configured service.':
    'Local provider. Messages and requested evidence stay on this machine.';
  if(data.external&&chatState.providerNotice!==data.baseURL) {
    $('chat-info').open=true;chatState.providerNotice=data.baseURL;
  }
  if(chatState.snapshot?.id!==data.snapshot?.id) {
    chatState.controller?.abort();chatState.lastNavigation=undefined;chatState.records=[];$('chat-messages').replaceChildren();
    chatState.snapshot=data.snapshot;chatState.report=report;
    if(data.snapshot) {
      const saved=sessionStorage.getItem('go-review-chat:'+data.snapshot.id);
      if(saved) {
        const records=JSON.parse(saved);
        if(!Array.isArray(records)||records.some(r=>!['user','assistant'].includes(r.role)||typeof r.content!=='string'))
          throw Error('Invalid saved chat history; use Clear chat.');
        for(const r of records) {
          const item=chatRecord(r.role,r.content);
          item.record.snapshotID=r.snapshotID||data.snapshot.id;
          chatCitations(item.article,r.content,item.record.snapshotID);
          if(r.role==='assistant'&&Array.isArray(r.activities)) {
            for(const activity of r.activities) {
              if(typeof activity.text!=='string'&&typeof activity.tool!=='string')throw Error('Invalid saved chat activity; use Clear chat.');
              chatActivity(item,activity);
            }
            item.activitySummary.textContent='Explored evidence';
          }
        }
      }
    }
  }
  $('chat-send').disabled=!data.enabled||!data.snapshot;
  chatNotice(!data.enabled?'Configure a provider in go-review/agent.json to enable chat.':
    !data.snapshot?'Open retained evidence first.':'Ready');
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
    if(action.snapshotID!==current.snapshot.id) {
      const target=current.snapshots?.find(snapshot=>snapshot.id===action.snapshotID);
      if(!target)throw Error('Evidence is no longer retained; start a new turn.');
      const sourceResponse=await fetch('/api/snapshot?snapshotID='+encodeURIComponent(target.id),{signal});
      const next=await sourceResponse.json();
      if(!sourceResponse.ok)throw Error(next.error||'Unable to read retained module');
      GoReviewChat.validate(next,action,target.id,typeof repositoryRun==='undefined'?null:repositoryRun);
      signal?.throwIfAborted();
      if(expectedFingerprint!==undefined&&chatFingerprint()!==expectedFingerprint)
        throw Error('View changed manually; automatic navigation cancelled.');
      const selectResponse=await fetch('/api/review/select',{method:'POST',signal,
        headers:{'Content-Type':'application/json'},body:JSON.stringify({snapshotID:target.id})});
      const selected=await selectResponse.json();
      if(!selectResponse.ok)throw Error(selected.error||'Unable to select retained module');
      signal?.throwIfAborted();
      if(expectedFingerprint!==undefined&&chatFingerprint()!==expectedFingerprint)
        throw Error('View changed manually; automatic navigation cancelled.');
      chatState.report=selected;chatState.snapshot=target;
      showReviewReport(selected);
      $('review-modules').value=selected.root;
      current.snapshot=target;
    }
    GoReviewChat.validate(report,action,current.snapshot.id,typeof repositoryRun==='undefined'?null:repositoryRun);
    if(action.view==='application')go({kind:'application'});
    else if(action.view==='back') {if(!history.length)throw Error('No parent view in navigation history');$('back').click();}
    else if(action.view==='packages')go({kind:'packages'});
    else if(action.view==='package')go({kind:'package',id:action.id});
    else if(action.view==='symbol'||action.view==='logical')go({kind:'logical',id:action.id});
    else if(action.view==='control'||action.view==='calls') {
      const previousControl=preferControl;
      preferControl=action.view==='control';go({kind:'symbol',id:action.id});
      if(history.length)history.at(-1).workspaceControl=previousControl;
    }
    else if(action.view==='dataflow')go({kind:'dataflow',id:action.id,value:action.valueID,
      direction:action.direction||'both',depth:action.depth??Infinity,all:!action.valueID});
    else if(action.view==='review-map')go({kind:'review',id:action.id,package:action.package,query:action.query,
      minComplexity:action.minComplexity,maxCoverage:action.maxCoverage,coverage:action.coverage,cohort:action.cohort});
    else if(action.view==='signals')go({kind:'signals',id:action.id,package:action.package});
    else if(action.view==='hygiene')go({kind:'signals',section:'hygiene',checkID:action.id});
    else if(['results','scope','limits'].includes(action.view)) {
      mode={...mode,reviewStepID:action.view==='results'?action.id:undefined};
      $({results:'review-results',scope:'scope-results',limits:'evidence'}[action.view]).click();
    }
    else if(action.view==='document')openDocumentation([...(report.documents||[]),...(report.entryPoints?.sources||[])].find(d=>d.id===action.id));
    else if(action.view==='architecture')go({kind:'system-flow',level:action.id,componentID:action.componentID,
      operationID:action.operationID,group:action.group});
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
$('chat-input').onkeydown=event=>{
  if(event.key==='Enter'&&!event.shiftKey&&!event.isComposing&&event.keyCode!==229) {
    event.preventDefault();
    if(!chatState.sending&&!$('chat-send').disabled)$('chat-form').requestSubmit();
  }
};
$('chat-form').onsubmit=async event=>{
  event.preventDefault();
  if(chatState.sending)return;
  const text=$('chat-input').value.trim();if(!text)return;
  chatState.sending=true;
  let assistant,completed=false,reader;
  try {
    const state=await chatLoad();
    if(!state.enabled||!state.snapshot)throw Error('Review assistant or evidence is unavailable');
    const prior=chatState.records.map(({role,content})=>({role,content}));
    chatRecord('user',text);assistant=chatRecord('assistant','');
    const allowNavigation=GoReviewChat.navigationRequested(text);
    let fingerprint=chatFingerprint();
    const context=chatContext();
    if(chatState.lastNavigation)context.lastConfirmedNavigation=chatState.lastNavigation;
    const controller=new AbortController();chatState.controller=controller;
    $('chat-send').disabled=true;$('chat-stop').disabled=false;$('chat-input').value='';
    chatWorking(assistant,'Working…');
    const response=await fetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json'},
      signal:controller.signal,body:JSON.stringify({message:text,snapshotID:context.kind==='changes'?undefined:state.snapshot.id,
        comparisonID:context.comparisonID,history:prior,
        context,allowNavigation})});
    if(!response.ok){const error=await response.json();throw Error(error.error||'Chat HTTP '+response.status);}
    reader=response.body.getReader();
    const decoder=new TextDecoder();let pending='',turnStart=0;
    while(true) {
      const part=await reader.read();
      pending+=decoder.decode(part.value||new Uint8Array(),{stream:!part.done});
      let end;
      while((end=pending.indexOf('\n'))>=0) {
        const messages=$('chat-messages');
        const follow=messages.scrollHeight-messages.scrollTop-messages.clientHeight<64;
        const line=pending.slice(0,end);pending=pending.slice(end+1);if(!line.trim())continue;
        const message=JSON.parse(line);
        if(message.type==='turn_start') {
          turnStart=assistant.record.content.length;chatWorking(assistant,'Working…');
        }
        else if(message.type==='discard_turn') {
          assistant.record.content=assistant.record.content.slice(0,turnStart);chatRender(assistant);
        }
        else if(message.type==='text') {assistant.record.content+=message.text;chatRender(assistant);chatWorking(assistant,'Writing…');}
        else if(message.type==='exploration') {chatExploration(assistant);chatWorking(assistant,'Exploring evidence…');}
        else if(message.type==='activity')chatWorking(assistant,message.text);
        else if(message.type==='tool'||message.type==='tool_error') {
          chatWorking(assistant,chatToolLabels[message.tool]||'Exploring evidence…');
          chatActivity(assistant,{tool:message.tool,args:message.args,text:message.text,
            error:message.type==='tool_error',snapshotID:message.args?.snapshotID||state.snapshot.id});
        } else if(message.type==='tool_done')chatWorking(assistant,'Working…');
        else if(message.type==='change_navigation') {
          const action=message.args.input;
          if(allowNavigation&&message.args.automatic){
            await changeNavigate(action,fingerprint,controller.signal);fingerprint=chatFingerprint();
          }else button('Show in Change Review',()=>changeAct(()=>changeNavigate(action)),assistant.article);
        }
        else if(message.type==='change_proposal') {
          const proposal=message.args;
          const disclosure=el('details',undefined,assistant.article);el('summary','Requested local action · '+proposal.action,disclosure);
          el('pre',JSON.stringify(proposal,null,2),disclosure);
          button('Review and confirm '+proposal.action,()=>changeAct(()=>changeApplyProposal(proposal)),disclosure);
        }
        else if(message.type==='navigation') {
          const action=message.navigation;
          if(allowNavigation&&action.automatic) {
            await chatNavigate(action,fingerprint,controller.signal);
            fingerprint=chatFingerprint();
          } else {
            const show=button('Show in diagram · '+(action.label||action.view),()=>chatNavigate(action),assistant.article);
            show.className='chat-navigation';
          }
        } else if(message.type==='error')throw Error(message.text);
        else if(message.type==='done')completed=true;
        if(follow)chatScroll();
      }
      if(part.done)break;
    }
    if(!completed)throw Error('Chat stream ended before completion.');
    chatCitations(assistant.article,assistant.record.content,state.snapshot.id);
    chatNotice('Ready');
  }catch(error){
    const reason=error.name==='AbortError'?'Stopped.':error.message;
    if(assistant)el('p',reason,assistant.article,'error');
    chatNotice(reason,error.name!=='AbortError');
    if(assistant&&!completed) {assistant.record.content+='\n[Response incomplete: '+reason+']';}
  }finally {
    if(assistant) {
      assistant.activity.classList.remove('working');
      assistant.activitySummary.textContent=completed?'Explored evidence':'Activity · interrupted';
      assistant.article.removeAttribute('aria-busy');
    }
    chatState.controller?.abort();
    if(reader)reader.releaseLock();
    chatState.controller=null;chatState.sending=false;$('chat-send').disabled=!chatState.status?.enabled;$('chat-stop').disabled=true;chatSave();
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
