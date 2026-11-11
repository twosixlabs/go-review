'use strict';
const documentationTools=GoReviewDocumentation;
const beforeDocumentationDetail=detail,beforeDocumentationRender=render,beforeDocumentationGo=go;

function documentationContext() {
  if(mode.kind==='endpoint') {
    const endpoint=selectedEntryPoint();
    const node=mode.detailLevel==='function'?endpointWalkthroughScene?.nodes.find(n=>n.id===mode.selected):
      overallEndpointScene?.nodes.find(n=>n.id===mode.selected);
    return {endpoint,symbolID:node?.symbolID||node?.ownerID||endpoint?.handlerID,location:node?.location};
  }
  if(mode.kind==='package')return {package:mode.id};
  if(['symbol','logical','dataflow','review'].includes(mode.kind))return {symbolID:mode.id};
  return {};
}
function openDocumentation(document,fragment='') {
  go({...mode,documentID:document.id,documentFragment:fragment,documentOpen:true,inspectorOpen:true,
    documentContext:documentationTools.contextKey(mode)});
}
function relatedDocumentation(parent) {
  const rows=documentationTools.relatedDocuments(report,documentationContext());
  const section=el('details',undefined,parent,'related-documentation');
  el('summary','Related documentation · '+rows.length,section);
  if(!rows.length)el('p','No explicit document associations found.',section,'muted');
  for(const row of rows) {
    const item=button(row.document.path,()=>openDocumentation(row.document),section);
    item.title=row.reasons.join('\n');
    el('p',row.reasons.join(' · '),section,'muted');
  }
  const first=documentationTools.overviewDocument(report);
  if(first)button('Browse all documentation · '+report.documents.length,()=>openDocumentation(first),section);
  else el('p','No Markdown collected from '+report.documentationRoot,section,'muted');
}
function documentationReader() {
  const document=documentationTools.overviewDocument(report,mode.documentID);
  beforeDocumentationDetail(document?.path||'Project documentation');
  if(mode.kind!=='application')button('Return to code',()=>{
    go({...mode,documentOpen:false});
  },$('detail'));
  if(!document) {
    el('p','No Markdown collected from '+report.documentationRoot,$('detail'),'muted');
    return;
  }
  const label=el('label','Document',$('detail'));label.htmlFor='document-selector';
  const selector=el('select',undefined,$('detail'));selector.id='document-selector';
  const documents=[...(report.documents||[]),...(report.entryPoints?.sources||[])];
  for(const doc of documents) {
    const option=el('option',doc.path,selector);option.value=doc.id;
  }
  selector.value=document.id;
  selector.onchange=()=>openDocumentation(documents.find(d=>d.id===selector.value));
  const entrySource=report.entryPoints?.sources?.some(d=>d.id===document.id);
  const root=entrySource?report.root:report.documentationRoot;
  el('p',root+'/'+document.path,$('detail'),'muted');
  const editor=el('a','Open in editor',$('detail'));
  editor.href='vscode://file/'+root+'/'+document.path;
  const content=el('article',undefined,$('detail'),'repository-document');
  if(entrySource) {
    el('pre',document.text,content);
    el('p','Retained entry-point evidence · SHA-256 '+document.sha256,$('detail'),'muted');
    return;
  }
  GoReviewMarkdown.render(content,document.text);
  const linkStatus=el('p',undefined,$('detail'),'muted');linkStatus.setAttribute('role','status');
  content.addEventListener('click',event=>{
    const link=event.target.closest('a[href]');
    if(!link||/^https?:\/\/|^mailto:/i.test(link.getAttribute('href')))return;
    event.preventDefault();
    const target=GoReviewMarkdown.documentLink(document.path,link.getAttribute('href'));
    const destination=target&&report.documents.find(d=>d.path===target.path);
    if(destination)openDocumentation(destination,target.fragment);
    else linkStatus.textContent='Document not collected: '+link.getAttribute('href');
  });
  if(mode.documentFragment) {
    let fragment=mode.documentFragment;
    try { fragment=decodeURIComponent(fragment); } catch { linkStatus.textContent='Invalid document fragment: '+fragment; }
    const heading=[...content.querySelectorAll('[id]')].find(h=>h.id==='document-heading-'+fragment);
    if(heading)$('detail').scrollTop=heading.offsetTop-$('detail').offsetTop;
    else linkStatus.textContent='Heading not found: '+fragment;
  } else $('detail').scrollTop=0;
}
detail=function(title,doc) {
  if(mode.documentOpen&&mode.documentContext!==documentationTools.contextKey(mode))
    mode={...mode,documentOpen:false};
  beforeDocumentationDetail(title,doc);
  if(report&&mode.kind!=='application')relatedDocumentation($('detail'));
};
documentDetail=function(document){openDocumentation(document);};
go=function(next,remember=true) {
  if(mode.kind==='application'&&next.kind==='endpoint')next={...next,overviewDocumentID:mode.documentID};
  if(mode.kind==='endpoint'&&next.kind==='application')next={...next,documentID:mode.overviewDocumentID};
  beforeDocumentationGo(next,remember);
};
render=function() {
  if(mode.documentOpen&&mode.documentContext!==documentationTools.contextKey(mode))
    mode={...mode,documentOpen:false};
  beforeDocumentationRender();
  if(report&&(mode.kind==='application'||mode.documentOpen))documentationReader();
};
$('docs').onclick=()=>{
  if(!report)return;
  const first=documentationTools.overviewDocument(report,mode.documentID);
  if(first)openDocumentation(first);
  else {mode={...mode,documentOpen:true,documentContext:documentationTools.contextKey(mode)};render();}
};
