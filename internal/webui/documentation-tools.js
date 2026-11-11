(function(root) {
  'use strict';
  const logical=typeof module!=='undefined'&&module.exports?require('./logical-tools.js'):root.GoReviewLogical;
  const normalize=path=>{
    const parts=[];
    for(const part of (path||'').replace(/\\/g,'/').split('/')) {
      if(part==='..')parts.pop();else if(part&&part!=='.')parts.push(part);
    }
    return parts.join('/');
  };
  const directory=path=>path.includes('/')?path.slice(0,path.lastIndexOf('/')):'';
  function contextKey(mode) {
    return JSON.stringify([mode.kind,mode.id,mode.selected,mode.step,mode.value]);
  }
  function overviewDocument(report,id) {
    const documents=report.documents||[];
    return documents.find(d=>d.id===id)||(report.entryPoints?.sources||[]).find(d=>d.id===id)||
      documents.find(d=>/^readme\.md$/i.test(d.path))||
      [...documents].sort((a,b)=>a.path.split('/').length-b.path.split('/').length||a.path.localeCompare(b.path))[0]||null;
  }
  function relatedDocuments(report,context={}) {
    const symbol=(report.symbols||[]).find(s=>s.id===context.symbolID);
    const file=(report.files||[]).find(f=>f.id===(context.location?.fileID||symbol?.location?.fileID));
    const pkg=context.package||symbol?.package||file?.package;
    const packagePath=pkg&&(pkg===report.module?'':pkg.startsWith(report.module+'/')?pkg.slice(report.module.length+1):pkg);
    const moduleRoot=normalize(report.root),docsRoot=normalize(report.documentationRoot||report.root);
    const modulePrefix=moduleRoot===docsRoot?'':moduleRoot.startsWith(docsRoot+'/')?moduleRoot.slice(docsRoot.length+1):null;
    const sourcePath=file?.path,packageName=pkg?.split('/').at(-1);
    const title=symbol&&logical.callableTitle(symbol);
    const mentions=(text,token)=>{
      if(!token)return false;
      const escaped=token.replace(/[.*+?^${}()|[\]\\]/g,'\\$&');
      return new RegExp('(^|[^\\w./-])'+escaped+'(?=$|[^\\w./-]|[.,](?:\\s|$))','m').test(text);
    };
    return (report.documents||[]).flatMap(document=>{
      const reasons=[],text=document.text||'',docDir=directory(document.path);
      if(file&&modulePrefix!==null) {
        const source=normalize(modulePrefix+'/'+file.path);
        for(const link of text.matchAll(/\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) {
          const target=link[1].split('#')[0];
          if(!/^[a-z][a-z0-9+.-]*:|^\//i.test(target)&&normalize(docDir+'/'+target)===source)
            reasons.push('Source path: '+source);
        }
      }
      const paths=[sourcePath,packagePath].filter(Boolean).flatMap(path=>
        [...new Set([path,modulePrefix===null?null:normalize(modulePrefix+'/'+path)].filter(Boolean))]);
      for(const path of paths) {
        const source=sourcePath&&(path===sourcePath||path===normalize(modulePrefix+'/'+sourcePath));
        if(mentions(text,path))reasons.push((source?'Source path: ':'Package path: ')+path);
      }
      if(pkg&&mentions(text,pkg))reasons.push('Package: '+pkg);
      if(packageName&&new RegExp('`'+packageName.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+'`').test(text))
        reasons.push('Package mention: '+packageName);
      if(title?.includes('.')&&mentions(text,title))reasons.push('Symbol: '+title);
      if(symbol?.kind==='function'&&packageName&&mentions(text,packageName+'.'+symbol.name))
        reasons.push('Symbol: '+packageName+'.'+symbol.name);
      const endpoint=context.endpoint;
      if(endpoint?.patternKnown&&mentions(text,endpoint.pattern))reasons.push('Route: '+endpoint.pattern);
      const localDirectory=file?directory(file.path):packagePath;
      if(localDirectory!==undefined&&localDirectory!==null&&modulePrefix!==null&&
        docDir===normalize(modulePrefix+'/'+localDirectory))
        reasons.push('Package documentation: '+docDir);
      return reasons.length?[{document,reasons:[...new Set(reasons)]}]:[];
    }).sort((a,b)=>a.document.path.localeCompare(b.document.path));
  }
  const api={overviewDocument,relatedDocuments,contextKey};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoReviewDocumentation=api;
})(typeof globalThis==='undefined'?this:globalThis);
