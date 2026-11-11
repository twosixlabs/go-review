(function(root) {
  'use strict';
  const maxBytes=6144,bytes=value=>{
    let size=0;
    for(const char of JSON.stringify(value)){const code=char.codePointAt(0);size+=code<128?1:code<2048?2:code<65536?3:4;}
    return size;
  };
  function page(values,args={}) {
    const offset=args.offset??0,limit=args.limit??10;
    if(!Number.isInteger(offset)||offset<0||!Number.isInteger(limit)||limit<1||limit>100)throw Error('Invalid comparison page: offset >= 0, limit 1-100');
    let size=0,end=offset;
    for(;end<values.length&&end-offset<limit;end++) {
      const count=bytes(values[end]);
      if(size+count>maxBytes){if(end===offset)throw Error('Comparison record exceeds page budget; request selected detail');break;}
      size+=count;
    }
    return {items:values.slice(offset,end),total:values.length,offset,nextOffset:end<values.length?end:null};
  }
  function text(value,args={}) {
    const chars=Array.from(value),offset=args.textOffset??0,limit=args.textLimit??2000;
    if(!Number.isInteger(offset)||offset<0||!Number.isInteger(limit)||limit<1||limit>6000)throw Error('Invalid comparison text page');
    let end=Math.min(chars.length,offset+limit);
    while(end>offset&&bytes(chars.slice(offset,end).join(''))>maxBytes)end--;
    return {text:chars.slice(offset,end).join(''),textPage:{total:chars.length,offset,nextOffset:end<chars.length?end:null}};
  }
  const summary=record=>{
    const {itemIDs,fileIDs,flowIDs,...rest}=record;
    return {...rest,itemCount:itemIDs.length,fileCount:fileIDs.length,flowCount:flowIDs.length};
  };
  function validate(c,args) {
    if(args.comparisonID&&args.comparisonID!==c.id)throw Error('Stale comparison; reload current Change Review');
    if(args.side&&!['before','after'].includes(args.side))throw Error('Revision side must be before or after');
    for(const [field,items] of [['moduleID',c.modules],['recordID',c.records],['flowID',c.flows],['fileID',c.files]]) {
      if(args[field]&&!items.some(item=>item.id===args[field]))throw Error('Unknown comparison '+field);
    }
    if(args.flowID&&args.moduleID&&c.flows.find(f=>f.id===args.flowID).moduleID!==args.moduleID)throw Error('Flow belongs to another module');
    if(args.recordID&&args.moduleID&&c.records.find(r=>r.id===args.recordID).moduleID!==args.moduleID)throw Error('Change belongs to another module');
    const record=c.records.find(r=>r.id===args.recordID);
    if(record&&args.fileID&&!record.fileIDs.includes(args.fileID))throw Error('File is outside selected change record');
    if(record&&args.flowID&&!record.flowIDs.includes(args.flowID))throw Error('Change is outside selected flow');
    if(record&&args.symbolID&&args.side&&record[args.side]?.symbolID!==args.symbolID)throw Error('Symbol is not the selected change owner on this side');
  }
  function query(c,state,operation,args={}) {
    validate(c,args);
    const records=c.records,flows=c.flows;
    if(args.status&&!['all','remaining','not-reviewed','reviewed','follow-up'].includes(args.status))throw Error('Unknown local review filter');
    if(args.perspective&&!['all','affected','shared','other'].includes(args.perspective))throw Error('Unknown change perspective');
    const itemIDs=c.files.flatMap(f=>f.items.map(i=>i.id));
    const progress={total:itemIDs.length,reviewed:itemIDs.filter(id=>state.items[id]==='reviewed').length,
      followUp:itemIDs.filter(id=>state.items[id]==='follow-up').length,flows:flows.length,
      considered:flows.filter(f=>state.flows[f.id]).length,version:state.version};
    let value;
    if(operation==='overview')value={comparisonID:c.id,revisions:c.revisions,scope:c.scope,environment:c.environment,
      createdAt:c.createdAt,progress,fileCount:c.files.length,recordCount:records.length,
      modules:page(c.modules,args),limitations:page(c.limitations,args),
      sections:['files','items','records','flows','record','witnesses','correspondences','progress','source','patch','flow','side']};
    else if(operation==='progress')value={...progress,items:page(itemIDs.map(id=>({id,status:state.items[id]||'not-reviewed'})),args)};
    else if(operation==='files'){
      const selected=c.files.filter(f=>(!args.query||[f.before.path,f.after.path].join(' ').toLowerCase().includes(args.query.toLowerCase()))&&
        (!args.status||args.status==='all'||f.items.some(i=>args.status==='remaining'?state.items[i.id]!=='reviewed':(state.items[i.id]||'not-reviewed')===args.status)));
      value={...page(selected.map(({items,...file})=>({...file,itemCount:items.length})),args),filteredOut:c.files.length-selected.length};
    }
    else if(operation==='items')value=page(c.files.filter(f=>!args.fileID||f.id===args.fileID).flatMap(f=>f.items.map(i=>({...i,status:state.items[i.id]||'not-reviewed'}))),args);
    else if(operation==='records') {
      const q=(args.query||'').trim().toLowerCase();
      const selected=records.filter(r=>(!args.moduleID||r.moduleID===args.moduleID)&&
        (!args.flowID||r.flowIDs.includes(args.flowID))&&(!args.fileID||r.fileIDs.includes(args.fileID))&&(!q||[r.title,r.reason].join(' ').toLowerCase().includes(q))&&
        (!args.perspective||args.perspective==='all'||(args.perspective==='shared'?r.flowIDs.length>1:args.perspective==='other'?r.flowIDs.length===0:r.flowIDs.length>0))&&
        (!args.status||args.status==='all'||r.itemIDs.some(id=>args.status==='remaining'?state.items[id]!=='reviewed':(state.items[id]||'not-reviewed')===args.status)));
      value={...page(selected.map(r=>({...summary(r),reviewed:r.itemIDs.filter(id=>state.items[id]==='reviewed').length})),args),filteredOut:records.length-selected.length};
    }else if(operation==='flows') {
      const selected=flows.filter(f=>(!args.moduleID||f.moduleID===args.moduleID)&&(!args.flowID||f.id===args.flowID)&&(!args.query||f.name.toLowerCase().includes(args.query.toLowerCase()))&&
        (!args.status||args.status==='all'||(args.status==='remaining'&&!state.flows[f.id])||
          f.associations.some(a=>records.find(r=>r.id===a.recordID)?.itemIDs.some(id=>args.status==='remaining'?state.items[id]!=='reviewed':(state.items[id]||'not-reviewed')===args.status))));
      value={...page(selected.map(({associations,...f})=>({...f,recordCount:new Set(associations.map(a=>a.recordID)).size,considered:!!state.flows[f.id]})),args),filteredOut:flows.length-selected.length};
    }else if(operation==='record') {
      const r=records.find(r=>r.id===args.recordID);if(!r)throw Error('Select recordID');
      value={...summary(r),items:page(r.itemIDs,args),files:page(r.fileIDs,args),flows:page(r.flowIDs,args)};
    }else if(operation==='witnesses') {
      const f=flows.find(f=>f.id===args.flowID);if(!f)throw Error('Select flowID');
      if(args.recordID&&!f.associations.some(a=>a.recordID===args.recordID))throw Error('Change is not associated with selected flow');
      const associations=f.associations.filter(a=>(!args.recordID||a.recordID===args.recordID)&&(!args.side||a.side===args.side));
      if(args.section==='steps'){
        const index=args.associationIndex??0;
        if(!Number.isInteger(index)||index<0||index>=associations.length)throw Error('Select a known associationIndex');
        value={...page(associations[index].witness,args),associationIndex:index,side:associations[index].side,quality:associations[index].quality};
      }else value=page(associations.map((a,index)=>({...a,witness:a.witness.slice(0,3),witnessCount:a.witness.length,associationIndex:index,sections:['steps']})),args);
    }else if(operation==='correspondences')value=page(c.correspondences.filter(m=>(!args.moduleID||m.moduleID===args.moduleID)&&(!args.symbolID||m.beforeID===args.symbolID||m.afterID===args.symbolID)),args);
    else if(operation==='annotations')value={nodes:annotate(c,args.flowID,args.side,args.nodes||[])};
    else throw Error('Unknown comparison operation: '+operation);
    value={comparisonID:c.id,...value};
    if(bytes(value)>32768)throw Error('Comparison result exceeds 32 KiB; select a smaller section/page');
    return value;
  }
  function annotate(c,flowID,side,nodes) {
    const flow=c.flows.find(f=>f.id===flowID);
    if(!flow)throw Error('Unknown comparison flow');
    const associations=flow.associations.filter(a=>a.side===side);
    return nodes.map(node=>{
      const direct=c.records.filter(r=>r[side]?.symbolID===node.symbolID&&associations.some(a=>a.recordID===r.id));
      const within=associations.filter(a=>a.witness.some(w=>w.from===node.symbolID||w.to===node.symbolID)).map(a=>a.recordID);
      if(!direct.length&&!within.length)return node;
      let label='Changes within';
      if(direct.length){
        const module=c.modules.find(m=>m.id===flow.moduleID);
        label=side==='after'&&direct.every(r=>!r.before)&&!module?.before?.error&&!module?.before?.partial?'Added':
          side==='before'&&direct.every(r=>!r.after)&&!module?.after?.error&&!module?.after?.partial?'Removed':'Changed';
      }
      const ids=[...new Set([...direct.map(r=>r.id),...within])];
      return {...node,change:{label,recordIDs:ids.slice(0,10),recordCount:ids.length,quality:'source-supported potential impact'}};
    });
  }
  const api={query,validate,page,text,annotate};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoReviewChanges=api;
})(typeof globalThis==='undefined'?this:globalThis);
