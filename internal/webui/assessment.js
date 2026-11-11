'use strict';
const beforeAssessmentRender=render,beforeAssessmentDetail=detail;

function openSignalResults(section,path,symbolID) {
  const finding=section==='architecture'&&symbolID&&GoReviewAssessment.contextFindings(report,{symbolID})[0];
  go({kind:'signals',package:section==='hygiene'?undefined:path,section,id:finding?.id});
}
function nodeSignalIndicators(group,context,x,y) {
  GoReviewAssessment.renderNodeIndicators(group,report,context,{
    x,y,onOpen:(section,path)=>openSignalResults(section,path,context.symbolID),
    drawGauge:(holder,options)=>drawDial(holder,'signal',{value:options.pointer?options.value:null},options)
  });
}
function signalSummary(parent,context={},interactive=true,includeMetrics=true) {
  parent.replaceChildren();
  parent.classList.add('overview-metrics');
  const symbol=context.symbolID?sym(context.symbolID):null;
  const scoped=!!(context.symbolID||context.package||context.location);
  const files=report.files.filter(f=>!scoped||(context.package?f.package===context.package:
    f.id===(context.location?.fileID||symbol?.location?.fileID)));
  const locations=symbol?symbol.location:files.map(f=>({fileID:f.id,line:1,column:1,endLine:Number.MAX_SAFE_INTEGER,endColumn:1}));
  const functions=report.symbols.filter(s=>['function','method','closure'].includes(s.kind)&&
    (!scoped||(context.package?s.package===context.package:s.id===context.symbolID)));
  const complexity=symbol?.cyclomatic??(functions.length?Math.max(...functions.map(s=>s.cyclomatic||0)):undefined);
  if(includeMetrics) {
    metricGauge(parent,'coverage',locations);
    const complexityGauge=metricGauge(parent,'complexity',null,complexity);
    if(!context.symbolID) {
      complexityGauge.querySelector('.metric-label').textContent='Complexity · max';
      complexityGauge.title+=' Scope maximum, not a sum or an average.';
    }
  }
  const state=GoReviewAssessment.indicators(report,context);
  const gauges=GoReviewAssessment.signalGauges(report,context);
  for(const [kind,gauge] of Object.entries(gauges)) {
    const target=el('span',undefined,parent,'signal-metric metric-gauge');
    target.style.setProperty('--metric-color',gauge.options.gaugeColor);
    if(interactive) {
      target.tabIndex=0;
      target.setAttribute('role','button');
      target.onclick=()=>openSignalResults(kind,state.path,context.symbolID);
      target.onkeydown=event=>{
        if(event.key==='Enter'||event.key===' ') {
          event.preventDefault();
          target.click();
        }
      };
    }
    target.title=gauge.title;target.setAttribute('aria-label',gauge.title);
    const canvas=svg('svg',{viewBox:'-28 -28 56 34',class:'metric-dial','aria-hidden':'true'},target);
    drawDial(canvas,'signal',{value:gauge.options.pointer?gauge.options.value:null},gauge.options);
    const caption=el('span',undefined,target,'metric-caption');
    el('strong',gauge.caption,caption);
    el('span',kind==='architecture'?'Architecture':gauge.label.replace(/^Hyg/,'Hygiene').replace(/ NR\b/,' not run').replace(/ E\b/,' errors'),caption,'metric-label');
  }
  if(interactive)el('span',state.scope+' · hygiene: snapshot',parent,'signal-scope');
}
function showSignalFinding(finding) {
  detail(finding.summary,finding.evidence);
  const rule=report.assessment.rules.find(r=>r.id===finding.ruleID);
  if(rule)el('p',rule.description,$('detail'),'muted');
  const target=sym(finding.symbolID);
  if(target&&['function','method','closure'].includes(target.kind))
    button('Explore '+label(target),()=>go({kind:'logical',id:target.id}),$('detail'));
  source(finding.location);
}
function contextualArchitecture() {
  if(!report||mode.kind==='application'||mode.kind==='signals'||mode.documentOpen)return;
  if(mode.kind==='endpoint'&&!mode.detailLevel)return;
  const context=documentationContext(),target=sym(context.symbolID);
  const file=report.files.find(f=>f.id===context.location?.fileID);
  const path=context.package||target?.package||file?.package;
  if(!path)return;
  const m=GoReviewAssessment.model(report,path);
  const findings=context.package?m.findings:GoReviewAssessment.contextFindings(report,context);
  const section=el('details',undefined,$('detail'),'contextual-architecture');
  el('summary','Architecture · '+GoReviewAssessment.labels[m.level]+' · '+findings.length+' findings',section);
  el('p','Package indicator · supported rules only. Complexity is separate.',section,'muted');
  if(!m.valid)el('p','Not assessed for this source snapshot. Analyze again to collect supported rules.',section);
  for(const finding of findings) {
    button(finding.summary,()=>showSignalFinding(finding),section);
    el('p',finding.evidence,section,'muted');
  }
  button('Review package signals',()=>go({kind:'signals',package:path}),section);
  const summary=el('div',undefined,$('detail'),'context-signal-summary');
  signalSummary(summary,context,true,false);
  $('detail').querySelector('h2')?.after(summary);
}
detail=function(title,doc) {
  beforeAssessmentDetail(title,doc);
  contextualArchitecture();
};
render=function() {
  if(!report)return;
  const showing=mode.kind==='signals'&&!mode.documentOpen;
  $('architecture-signals').setAttribute('aria-pressed',String(showing));
  $('hygiene').setAttribute('aria-pressed',String(showing&&mode.section==='hygiene'));
  document.body.classList.toggle('signals-home',showing);
  signalSummary($('signal-summary'));
  if(!showing){beforeAssessmentRender();return;}
  document.body.classList.remove('application-home');
  $('application').setAttribute('aria-pressed','false');
  for(const id of ['logical-flow','function-flow','data-flow','call-map','review-map'])
    $(id).setAttribute('aria-pressed','false');
  $('list').replaceChildren();$('graph').replaceChildren();activeScene=null;
  $('graph').style.display='none';$('application-overview').hidden=false;
  $('canvas').querySelector('.tools').hidden=true;document.querySelector('.graph-hint').hidden=true;
  $('breadcrumb').textContent=report.module+' / architecture signals'+(mode.package?' / '+short(mode.package):'');
  $('compact-statements').disabled=true;
  button('All package signals',()=>go({kind:'signals'}),$('list'));
  for(const p of GoReviewAssessment.model(report).packages)
    button(GoReviewAssessment.labels[p.level]+' · '+short(p.package),()=>go({kind:'signals',package:p.package}),$('list'));
  GoReviewAssessment.render($('application-overview'),report,{
    package:mode.package,onPackage:path=>go({kind:'signals',package:path}),
    renderMetrics:(parent,context)=>signalSummary(parent,context,false),
    onFinding:finding=>{mode={...mode,id:finding.id};showSignalFinding(finding);},
    onLocation:location=>{detail('Hygiene evidence');source(location);}
  });
  if(mode.section==='hygiene') {
    const panel=$('application-overview'),target=$('hygiene-results');
    if(target)panel.scrollTop=target.offsetTop-panel.offsetTop;
  }
  $('hygiene').setAttribute('aria-pressed',String(mode.section==='hygiene'));
  const selected=GoReviewAssessment.model(report,mode.package).findings.find(f=>f.id===mode.id);
  if(selected)showSignalFinding(selected);
  else detail('Architecture signals','Select a finding to inspect its evidence and source. Indicators summarize supported rules only.');
};
$('architecture-signals').onclick=()=>{if(report)go({kind:'signals'});};
$('hygiene').onclick=()=>{if(report)openSignalResults('hygiene');};
