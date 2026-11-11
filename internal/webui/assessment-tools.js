(function(root) {
  'use strict';
  const labels={'clear':'Clear','watch':'Watch','concern':'Concern','high-concern':'High concern','not-assessed':'Not assessed'};
  const rank={'not-assessed':-1,clear:0,watch:1,concern:2,'high-concern':3};
  const graph=typeof module!=='undefined'&&module.exports?require('./graph-tools.js'):root.GoReviewGraph;
  function model(report,path) {
    const assessment=report.assessment;
    const valid=assessment?.version==='architecture-signals/1'&&typeof report.sourceSHA256==='string'&&
      report.sourceSHA256.length>0&&assessment.sourceSHA256===report.sourceSHA256;
    const packages=valid?(assessment.packages||[]).filter(p=>!path||p.package===path):[];
    const assessed=packages.filter(p=>p.level!=='not-assessed'&&labels[p.level]);
    const level=assessed.reduce((worst,p)=>rank[p.level]>rank[worst]?p.level:worst,'not-assessed');
    const findings=valid?(assessment.findings||[]).filter(f=>!path||f.package===path):[];
    const checks=valid?assessment.checks||[]:[];
    return {valid,packages,level,assessed:assessed.length,total:packages.length,findings,
      hygiene:{passed:checks.filter(c=>c.status==='pass').length,
        completed:checks.filter(c=>['pass','fail'].includes(c.status)).length,
        errors:checks.filter(c=>c.status==='error').length,notRun:checks.filter(c=>c.status==='not-run').length},checks};
  }
  function contextFindings(report,context) {
    return model(report,context.package).findings.filter(f=>{
      if(context.symbolID)return f.symbolID===context.symbolID;
      const loc=context.location;
      return loc&&loc.fileID===f.location.fileID&&f.location.line<=(loc.endLine||loc.line)&&(f.location.endLine||f.location.line)>=loc.line;
    });
  }
  function indicators(report,context={}) {
    const global=model(report),symbol=(report.symbols||[]).find(s=>s.id===context.symbolID);
    const file=(report.files||[]).find(f=>f.id===(context.location?.fileID||symbol?.location?.fileID));
    const path=context.package||symbol?.package||file?.package;
    const scoped=model(report,path);
    let level='not-assessed',scope=context.package?'Package':context.symbolID?'Function':context.location?'Source':report.review?.moduleCount>1?'Module':'Repository';
    const explicit=context.symbolID||context.location||context.package;
    if(!explicit)level=global.level;
    else if(context.package)level=scoped.level;
    else if(path&&file&&scoped.level!=='not-assessed'&&!file.path.endsWith('_test.go')&&
      !/^\/\/ Code generated .* DO NOT EDIT\.\r?$/m.test(file.source||'')) {
      level='clear';
      for(const f of contextFindings(report,context)) {
        const severity={low:'watch',medium:'concern',high:'high-concern'}[f.severity];
        if(rank[severity]>rank[level])level=severity;
      }
    }
    const h=global.hygiene;
    const hygieneLevel=!global.valid?'not-assessed':h.errors?'concern':h.passed<h.completed?'concern':
      !h.completed?'not-assessed':h.notRun?'watch':'clear';
    const hygieneText=!global.valid?'Hyg Not assessed':`Hyg ${h.passed}/${h.completed}${h.errors?' · '+h.errors+' E':''}${h.notRun?' · '+h.notRun+' NR':''}`;
    return {level,path,scope,hygieneLevel,hygieneText,
      architectureTitle:`${scope} architecture: ${labels[level]}. Supported-rule signals only; complexity is separate.`,
      hygieneTitle:global.valid?`Snapshot-wide hygiene: ${h.passed}/${h.completed} completed checks passed · ${h.errors} errors · ${h.notRun} not run. Not a per-function result.`:'Hygiene not assessed for this source snapshot.'};
  }
  function renderNodeIndicators(group,report,context={},options={}) {
    group.querySelector('.node-signals')?.remove();
    if(!context.symbolID&&!context.package&&!context.location)context={symbolID:'unresolved-node'};
    const document=group.ownerDocument,NS='http://www.w3.org/2000/svg',state=indicators(report,context);
    const gauges=signalGauges(report,context);
    const holder=document.createElementNS(NS,'g');holder.setAttribute('class','node-signals');
    holder.setAttribute('transform',`translate(${options.x||0},${options.y||0})`);
    group.append(holder);
    for(const [index,signal] of ['architecture','hygiene'].entries()) {
      const badge=document.createElementNS(NS,'g');
      badge.setAttribute('class','signal-link level-'+(index?state.hygieneLevel:state.level));
      badge.setAttribute('transform',`translate(${index*76},0) scale(.5)`);
      badge.dataset.signal=signal;badge.setAttribute('role','button');badge.setAttribute('tabindex','0');
      const gauge=gauges[signal],title=gauge.title;
      badge.setAttribute('aria-label',title);
      const dial=document.createElementNS(NS,'g');dial.setAttribute('class','signal-dial');badge.append(dial);
      options.drawGauge(dial,gauge.options);
      const caption=document.createElementNS(NS,'text');caption.setAttribute('x',34);caption.setAttribute('y',0);
      caption.textContent=gauge.caption;badge.append(caption);
      const label=document.createElementNS(NS,'text');label.setAttribute('x',34);label.setAttribute('y',16);
      label.setAttribute('class','signal-label');label.textContent=gauge.label;badge.append(label);
      const tooltip=document.createElementNS(NS,'title');tooltip.textContent=title;badge.append(tooltip);
      const activate=e=>{e.preventDefault();e.stopPropagation();options.onOpen?.(signal,state.path);};
      badge.addEventListener('click',activate);
      badge.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' ')activate(e);});
      holder.append(badge);
    }
    return state;
  }
  function signalGauges(report,context={}) {
    const state=indicators(report,context),h=model(report).hygiene;
    const value=rank[state.level]<0?null:rank[state.level];
    const colors={clear:'#159447',watch:'#f4cb32',concern:'#f59b23','high-concern':'#ef4444','not-assessed':'#465367'};
    const architecture={...graph.gaugeOptions('coverage',value),max:3,gaugeColor:colors[state.level],levelColors:[colors[state.level]]};
    const measured=model(report).valid&&h.completed>0;
    const hygiene=graph.gaugeOptions('coverage',measured?h.passed/h.completed*100:null);
    if(measured) {
      const color=h.errors?colors.concern:h.passed<h.completed?colors['high-concern']:h.notRun?colors.watch:colors.clear;
      hygiene.gaugeColor=color;hygiene.levelColors=[color];
    }
    return {
      architecture:{options:architecture,caption:labels[state.level],label:'Arch',title:state.architectureTitle},
      hygiene:{options:hygiene,caption:measured?`${h.passed}/${h.completed}`:'Not assessed',
        label:'Hyg'+(h.errors?' · '+h.errors+' E':'')+(h.notRun?' · '+h.notRun+' NR':''),title:state.hygieneTitle}
    };
  }
  function renderMarkerIndicator(group,report,contexts,radius) {
    const states=contexts.map(context=>indicators(report,context));
    const level=states.reduce((worst,state)=>rank[state.level]>rank[worst]?state.level:worst,'not-assessed');
    const unknown=states.filter(state=>state.level==='not-assessed').length;
    const circle=group.ownerDocument.createElementNS('http://www.w3.org/2000/svg','circle');
    circle.setAttribute('class','architecture-marker level-'+level);
    for(const [attr,value] of Object.entries({cx:radius,cy:-radius,r:4}))circle.setAttribute(attr,value);
    const title=group.ownerDocument.createElementNS('http://www.w3.org/2000/svg','title');
    title.textContent=`Architecture: ${labels[level]} · ${contexts.length} members · ${unknown} not assessed. Highest member concern; marker position and coverage fill are unchanged.`;
    circle.append(title);group.append(circle);
  }
  function render(container,report,options={}) {
    const document=container.ownerDocument,m=model(report,options.package);
    const add=(tag,text,parent=container,cls)=>{
      const node=document.createElement(tag);if(text!==undefined)node.textContent=text;
      if(cls)node.className=cls;parent.append(node);return node;
    };
    const button=(text,action,parent=container)=>{
      const node=add('button',text,parent);node.type='button';node.onclick=action;return node;
    };
    container.replaceChildren();
    add('h1','Architecture signals');
    const summary=add('div',undefined,container,'assessment-summary');
    if(options.renderMetrics)options.renderMetrics(summary,options.package?{package:options.package}:{});
    else add('p',labels[m.level],summary,'architecture-indicator level-'+m.level);
    if(!m.valid) {
      add('p','Not assessed for this source snapshot. Analyze again to collect supported rules.');return;
    }
    add('p',`${m.assessed}/${m.total} packages assessed · ${m.findings.length} findings. ${report.review?.moduleCount>1?'Module':'Repository'} indicator uses the highest package concern, not an average.`);
    add('p','Heuristic review flags, not proven defects. Clear means no supported-rule matches, not a clean bill of architectural health. Complexity, function size, coverage and hygiene do not contribute to this indicator.',container,'muted');
    const hygiene=add('section');hygiene.id='hygiene-results';
    add('h2','Mechanical hygiene',hygiene);
    const h=m.hygiene;
    add('p',`${h.passed}/${h.completed} completed checks passed · ${h.errors} error${h.errors===1?'':'s'} · ${h.notRun} not run. Snapshot-wide results, not per-function measurements. Each check identifies its source, module or repository scope.`,hygiene);
    const checkLabels={pass:'Pass',fail:'Fail',error:'Error','not-run':'Not run'};
    for(const check of m.checks) {
      const row=add('details',undefined,hygiene);row.dataset.check=check.id;
      row.open=options.checkID===check.id;
      add('summary',`${checkLabels[check.status]||check.status} · ${check.title}`,row);
      add('pre',check.detail,row);
      for(const location of check.locations||[]) {
        const file=(report.files||[]).find(f=>f.id===location.fileID);
        button(`${file?.path||location.fileID}:${location.line}`,()=>options.onLocation?.(location),row);
      }
    }
    if(options.package)button('All packages',()=>options.onPackage?.(''));
    const packages=add('section');add('h2','Packages',packages);
    for(const p of m.packages) {
      const row=button(p.package,()=>options.onPackage?.(p.package),packages);
      row.dataset.package=p.package;
      row.className='signal-package';
      if(options.renderMetrics)options.renderMetrics(add('span',undefined,row,'package-metrics'),{package:p.package});
      else add('span',labels[p.level]||labels['not-assessed'],row,'level-'+p.level);
      if(p.reason)add('p',p.reason,packages,'muted');
    }
    add('h2','Findings');
    if(!m.findings.length)add('p',m.assessed?'No matches in the supported rules.':'No assessed production source.');
    const severity={high:3,medium:2,low:1};
    for(const finding of [...m.findings].sort((a,b)=>severity[b.severity]-severity[a.severity]||a.package.localeCompare(b.package)||a.location.line-b.location.line)) {
      const item=add('section',undefined,container,'signal-finding');
      const row=button(`${finding.severity.toUpperCase()} · ${finding.summary}`,()=>options.onFinding?.(finding),item);
      row.dataset.finding=finding.id;
      add('p',finding.evidence,item);
      const file=(report.files||[]).find(f=>f.id===finding.location.fileID);
      add('p',`${file?.path||finding.package}:${finding.location.line} · ${finding.ruleID}`,item,'muted');
    }
    const rules=add('details');add('summary','Rules & indicator thresholds',rules);
    add('p','One high finding → High concern; one medium → Concern; isolated low → Watch. Three distinct affected owners covering at least 20% of assessed function/interface declarations raise low findings to Concern, or medium findings to High concern. Several findings on one owner do not increase recurrence. A scope with no eligible declarations or a parse error is Not assessed.',rules);
    for(const rule of report.assessment.rules||[]) {
      add('h3',rule.title,rules);add('p',rule.description,rules);
    }
    add('p','Ownership, meaningful boundaries, persistence policy, error semantics and goroutine lifecycle still require engineer review. Generated files, tests, excluded build tags and dependencies are outside this assessment.',rules);
    add('p',`${report.assessment.version} · source ${report.sourceSHA256} · commit ${report.revision?.commit||'unavailable'}${report.revision?.dirty?' (dirty)':''}`,rules,'muted');
  }
  const api={labels,rank,model,contextFindings,indicators,signalGauges,renderNodeIndicators,renderMarkerIndicator,render};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoReviewAssessment=api;
})(globalThis);
