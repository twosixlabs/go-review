'use strict';
const tutorialKey='go-review-tutorial:v1';
button('Tutorial',()=>tutorialStart(),document.querySelector('.workspace-bar')).id='workspace-tutorial';
const tutorialLayer=el('div',undefined,document.body,'tutorial-layer');
tutorialLayer.hidden=true;
const tutorialSpotlight=el('div',undefined,tutorialLayer,'tutorial-spotlight');
tutorialSpotlight.setAttribute('aria-hidden','true');
const tutorialDialog=el('section',undefined,tutorialLayer,'tutorial-dialog');
tutorialDialog.id='tutorial-dialog';tutorialDialog.hidden=true;
tutorialDialog.setAttribute('role','dialog');tutorialDialog.setAttribute('aria-modal','true');
tutorialDialog.setAttribute('aria-labelledby','tutorial-title');
tutorialDialog.setAttribute('aria-describedby','tutorial-description');
const tutorialProgress=el('p',undefined,tutorialDialog,'muted');
tutorialProgress.id='tutorial-progress';tutorialProgress.setAttribute('aria-live','polite');
const tutorialTitle=el('h2',undefined,tutorialDialog);tutorialTitle.id='tutorial-title';
const tutorialDescription=el('p',undefined,tutorialDialog);tutorialDescription.id='tutorial-description';
const tutorialNotice=el('p',undefined,tutorialDialog,'error');tutorialNotice.setAttribute('role','status');
const tutorialActions=el('div',undefined,tutorialDialog,'tutorial-actions');
const tutorialSkip=button('Skip tutorial',()=>tutorialClose(),tutorialActions);tutorialSkip.id='tutorial-skip';
const tutorialBack=button('Back',()=>{tutorialIndex--;tutorialShow();},tutorialActions);tutorialBack.id='tutorial-back';
const tutorialNext=button('Next',()=>{
  if(tutorialIndex===tutorialSteps.length-1)tutorialClose();
  else {tutorialIndex++;tutorialShow();}
},tutorialActions);tutorialNext.id='tutorial-next';
let tutorialSteps=[],tutorialIndex=0,tutorialReturnFocus=null,tutorialStorageError='';

function tutorialContext() {
  if(typeof changeState!=='undefined'&&changeState.active)return ['Explore Change Review','Compare pinned Before and After revisions through affected flows, shared implementation and complete supporting changes.','#change-heading'];
  if(!report)return ['Start with a repository','Use Repository & scope to choose a trusted repository. Review runs authorized checks and tests; Analyze only collects static evidence without running target tests.','#workspace-setup'];
  if(mode.documentOpen)return ['Read documentation in context','The reader shows collected project documentation. Return to code or Close details takes you back without losing your code context.','#detail'];
  if(mode.workspacePanel)return ['Inspect review evidence','This reader explains the current review results, applied scope, or analysis limits. Missing and skipped checks are not passing results.','#detail'];
  switch(mode.kind) {
    case 'application':return ['Start from Application','Choose an entry point to follow its overall flow. Startup & wiring is separate from application operations.','#canvas'];
    case 'endpoint':return ['Follow this entry-point flow','Select a card for its source-backed evidence. Expand reveals details inside their parent; Focus flow drills deeper while retaining parent context.','#canvas'];
    case 'packages':case 'package':return ['Explore packages and functions','Choose a package, then a function. Use the explorer to filter functions instead of fitting the entire codebase into one diagram.','#list'];
    case 'review':return ['Compare measured evidence','Review Map compares coverage and complexity. Select a marker, then a member if several functions share it. Unknown coverage is not zero.','#canvas'];
    case 'signals':return ['Inspect findings, not just scores','Choose a package or finding to read its explanation and source evidence. Hygiene reports snapshot-wide check outcomes.','#canvas'];
    case 'system-flow':return ['Explore structure and exchanges','Choose a component group, component, and operation to reach its implementation. These arrows describe typed exchanges, not execution order.','#canvas'];
    case 'logical':return ['Follow local implementations','Logical Flow shows one function and its local calls or function-value references. Select a target to drill down; external and unresolved calls remain explicit boundaries.','#canvas'];
    case 'dataflow':return ['Trace definitions and uses','Data Flow shows source-linked value definitions and consumers. It is static analysis, not recorded runtime values or proof of a tested path.','#canvas'];
    default:return ['Inspect this function','Control Flow shows possible statement paths; Call Relationships shows static callers and callees. Neither is a recorded execution trace.','#canvas'];
  }
}

function tutorialLessons() {
  const lesson=(title,text,target='#canvas')=>[title,text,target];
  if(typeof changeState!=='undefined'&&changeState.active)return [
    lesson('Confirm what is being compared','Resolve local committed refs or fetch a source from a Git remote. Optionally compare from the merge base; inspect the pinned commits and confirm static collection.','#change-inputs'),
    lesson('Start with affected behavior','Select a flow, then an annotated stage or its Changes list. A hidden helper remains reviewable even when the visible diagram has not changed. Before and After are separate source-backed scenes, not an execution trace.','#change-flow'),
    lesson('Read implementation in context','Shared implementation is counted once across its associated callers. Open retained Before/After source, targeted patch, source witnesses or revision-specific logical/control/data evidence. Other changes and Complete Git inventory retain material with no semantic mapping.','#change-perspectives'),
    lesson('Track implementation and consequences separately','Reading never marks anything reviewed. Explicit reversible actions affect the displayed item IDs. Flow-context consideration is separate; neither state is an approval or proof of correctness. Chat navigation cannot authorize collection or progress changes.','#change-progress')
  ];
  if(!report)return [
    lesson('Choose what is allowed to run','Open Repository & scope and enter the repository path. Review collects checks and in-scope unit tests. Analyze only is the choice for static exploration without executing target tests. Saved exclusions and test selections affect later reviews.','#workspace-setup'),
    lesson('Wait for evidence before interpreting it','Review progress names the current stage. After completion, open Review results to distinguish passed checks, failures, and checks that were not run. A diagram alone does not establish that tests passed.','#status'),
    lesson('Choose a starting question','Use Application for entry-point behavior, Components & exchanges for structural dependencies, Packages for implementation browsing, and Review Map for coverage-versus-complexity investigation.','.review-sections')
  ];
  if(mode.documentOpen)return [
    lesson('Read the project before judging the code','Use the Document selector to choose a collected document. Follow collected relative links to related documents. A document describes intended behavior; compare that intent with the implementation rather than assuming they agree.','#detail'),
    lesson('Connect the document to implementation','Related documentation in code details links documents associated with the selected context. Open in editor opens the document itself. Return to code restores the underlying code view.','#detail'),
    lesson('Keep the evidence boundary clear','Only collected documents and entry-definition sources are available here. A missing link means the target was not collected, not that the project has no documentation.','#detail')
  ];
  if(mode.workspacePanel)return [
    lesson('Read outcomes and scope together','Review results separates module outcomes and check evidence; Applied scope explains exclusions and selections. Read both before interpreting a missing result as a problem or a passing result as complete coverage.','#detail'),
    lesson('Investigate a failed or skipped check','Read the command, output, and reason where provided. Failed means the check reported a problem; skipped or not run means there is no result. Collection is explicit through Repository & scope or View options, never triggered by this tutorial.','#detail'),
    lesson('Know what the analysis cannot establish','Analysis limits explains unresolved or unsupported evidence. Use these limits to qualify your review conclusions rather than treating an incomplete graph as a complete account of runtime behavior.','#detail')
  ];
  switch(mode.kind) {
    case 'application':return [
      lesson('Read the entry-point inventory','Each entry card is an externally reachable starting point, not an internal function list. Depending on the project, entries come from HTTP registrations, WIT and component configuration, CLI main, or a library public API. Startup & wiring is listed separately.','#application-overview'),
      lesson('Choose a behavior to investigate','Use Find entry point to narrow the inventory by name, interface, or path. Select an entry card to open its overall flow. Start with the operation relevant to your review rather than opening the entire implementation at once.','#application-overview'),
      lesson('Then follow the operation into evidence','In the opened flow, select a stage to read its source and measurements in Details. Expand reveals contained stages; Focus flow opens a deeper operation with parent context. Follow this sequence: entry point → stage → implementation → source.','#application-overview')
    ];
    case 'endpoint':return [
      lesson('Read the flow before expanding it','Cards are source-owned operation groups. The main lane follows source continuation; side branches show alternatives. Dashed async relationships do not promise completion order or a return join. Placement is not evidence that a path ran.','#activity-overview'),
      lesson('Select, Expand, or Focus','Select a card title to inspect its evidence. Expand reveals child stages inside the parent so you keep the surrounding flow visible. Focus flow opens that operation separately and retains a parent map. Use Back or Return to parent flow to return.','#activity-overview'),
      lesson('Check the evidence behind a stage','In Details, read source alternatives, unresolved boundaries, commentary, and Measurement scope. A grouped card represents multiple definitions: inspect its members before attributing the group measurement to one function. Open source to verify the behavior.','#workspace-evidence')
    ];
    case 'packages':case 'package':return [
      lesson('Distinguish imports from calls',mode.kind==='packages'?
        'Each card is a package; arrows are import dependencies. An import does not tell you which function runs first, or even whether a particular path executes. Select a package card or its explorer entry to see its functions.':
        'You are inside one package. Function cards and their arrows show static call relationships, not import dependencies or execution order. Use the function explorer to choose a review-sized starting point.'),
      lesson('Find a function rather than shrinking the graph','Select a package, then use Filter functions in the explorer. Selecting a function opens Logical Flow for a manageable neighborhood of implementations. Use Fit all only when you want the complete structural map; zoom back in to read cards.','#list'),
      lesson('Follow a question into the implementation','Use Logical Flow to understand collaborators, Control Flow to inspect branches, Data Flow to trace definitions and uses, or Call Relationships to inspect callers and callees. Package breadcrumbs return to the function catalog.','.function-views')
    ];
    case 'review':return [
      lesson('Read the two axes','The horizontal axis is raw cyclomatic complexity: farther right means more branching structure. The vertical axis is statement coverage: higher means more measured statements executed. The unknown band is missing coverage evidence, not zero coverage.'),
      lesson('Resolve grouped markers before drawing conclusions','A numbered marker contains functions with identical measurements. Select it, then choose a member in the explorer. Package and Name filters narrow the population; the list is ordered by complexity. Filtering clears the current function selection.','#list'),
      lesson('Investigate the cause, not just the position','Select a function, then Explore control flow to understand branches, Explore logical flow for collaborators, or Explore value data flow for dependencies. The corner architecture indicator is a separate supported-rule signal. Coverage does not establish assertion quality or branch coverage.','#workspace-evidence')
    ];
    case 'logical':return [
      lesson('Read calls and function references differently','The selected implementation is the starting card. Solid arrows lead to resolved local calls. Dashed function-value references mean a function was passed, assigned, or registered; they do not prove invocation. Repeated sites share a target card but retain their source evidence.'),
      lesson('Reveal the neighborhood you need','The explorer separates other-package calls, same-package calls, and function values. Use Expand same-package calls to reveal hidden local helpers and the filter to match implementation names or source text. Pagination limits clutter; it does not remove targets.','#list'),
      lesson('Follow a target and verify its source','Select a target card to make it the current function. In Details, inspect Called from, Declaration & source, and Inspect function source. Boundary cards expose external or dynamic sites that cannot be followed as verified local implementations. Use Back to return to the previous function.','#workspace-evidence')
    ];
    case 'dataflow':return [
      lesson('Read definitions and consumers','Cards are SSA values or operations; arrows connect definitions to their consumers. The initial neighborhood is a slice around a selected value, not every statement in the function. An orange boundary marks analysis limits or effects, not automatically a defect.'),
      lesson('Trace the value in either direction','Choose a value in the explorer or graph. In Details, Trace upstream definitions answers where it comes from; Trace downstream uses answers what depends on it. Direct dependencies & uses returns to one level; Expand one level broadens the slice.','#workspace-evidence'),
      lesson('Inspect source and cross-function boundaries','Show whole function removes the slice restriction. Read Defined from and use evidence, types, source locations, and limitations. A known callee offers its Control flow and Data flow. Static dependencies do not prove runtime values, exact object identity, or a tested path.','#workspace-evidence')
    ];
    case 'signals':return mode.section==='hygiene'?[
      lesson('Read check outcomes, not a local score','Hygiene describes snapshot-wide checks, not the selected function. Passed, failed, errored, skipped, and not run are different outcomes. A skipped check supplies no assurance; a collection error is not itself a code defect.'),
      lesson('Open the check evidence','Read each check result and its available command, output, and explanation. Determine whether it reported a source problem or could not run. Review results provides the broader module and scope context.'),
      lesson('Act only on evidence you can verify','For a source diagnostic, inspect the reported location. For an unavailable check, read its reason before deciding whether to rerun anything. Repository & scope governs execution; this tour never launches checks.','#workspace-evidence')
    ]:[
      lesson('Read architecture signals as supported rules','Package cards summarize findings from the rules actually assessed. Clear means no supported-rule findings in that scope, not a clean bill of architectural health. Not assessed is missing evidence. Complexity is measured separately.'),
      lesson('Select a finding and inspect the reason','Choose a package in the explorer to narrow findings, then select a finding. Details shows its explanation, supporting evidence, rule description, and source location. Read the source before deciding whether it warrants a change.','#list'),
      lesson('Follow the implicated implementation','When a finding names a function, Explore opens its Logical Flow. Check collaborators and Control Flow to understand the cause, then use Back to return to the finding. Do not treat a colored indicator as a substitute for this investigation.','#workspace-evidence')
    ];
    case 'system-flow':return [
      lesson('Read boundaries and contracts','System context shows the module and neighboring actors or boundaries. Components & exchanges shows source-derived groups and typed information contracts. Arrows describe information direction; dashed exchanges are unresolved, inferred, or asynchronous as the legend states, not a timeline.'),
      lesson('Drill from a group to an operation','Choose a component group, then a component, then a named operation in the explorer. All relevant alternatives broadens the operation view. Selecting an operation with a resolved implementation enables the function perspectives.','#list'),
      lesson('Verify the exchange against source','Details exposes the information types, alternative source evidence, resolution, and provenance. Follow source locations to verify a contract, or switch to Logical Flow for the implementation. A package grouping is structural navigation, not a certified business boundary.','#workspace-evidence')
    ];
    default:return preferControl?[
      lesson('Read possible statement paths','Cards are statements or grouped steps. Arrows show possible next steps; branch labels distinguish alternatives and loop arrows return to earlier steps. Statement coverage colors reflect recorded block counts, not proof that every branch was tested.'),
      lesson('Inspect a branch at source level','Select a step to read its statement, comments, coverage, and source in Details. Follow possible next step inspects a successor. Expand grouped statements to see individual steps; View options controls grouping, comments, and unreachable blocks.','#workspace-evidence'),
      lesson('Follow a call without losing the caller','Calls at this step links resolved implementations; unresolved calls remain source evidence. Use Logical Flow for a less detailed collaborator view, or Data Flow to ask which definitions influence a branch or return. Back returns to the caller.','.function-views')
    ]:[
      lesson('Read callers and possible callees','The focused card is the selected function. Arrows represent static call relationships, not a recorded call sequence. External and unresolved targets have no verified local implementation; function values do not prove execution.'),
      lesson('Choose the relationship to investigate','Select a local function card or a caller/callee entry in Details to follow it. Read the call kind and source evidence before assuming a dynamic target. View options can reveal external calls.','#workspace-evidence'),
      lesson('Choose the next level of detail',"Logical Flow gives a filtered implementation neighborhood. Control Flow exposes the selected function's branches and statements; Data Flow follows its definitions and uses. Use source to verify the call and Back to return to the prior function.",'.function-views')
    ];
  }
}

function tutorialBuildSteps() {
  return [
    ['Choose the view for your question','Application answers where behavior starts. System context and Components & exchanges explain structure. Packages finds implementations. Assess investigates measured concerns; Evidence explains the collection and its limits. Start with one question and drill down rather than treating a full graph as the review.','.review-sections'],
    tutorialContext(),...tutorialLessons(),
    ['Interpret measurements in their scope',"Coverage is executed statements, not test quality. Complexity is branching count; a group's CC is a maximum, not a sum. Architecture reports supported-rule concerns. Hygiene is snapshot-wide. Gray or ? means unknown or unassessed, not failure. Read the scope label and select evidence before making a judgment.",'#signal-summary'],
    ['Navigate without losing your place','Scroll pans; Shift-scroll or pinch zooms. Drag background to pan and nodes to rearrange. Fit shows the full graph, not necessarily readable labels. Use breadcrumbs for ancestors and Back for the previous context. Details shows the selected evidence; closing it does not erase the selection.','#canvas'],
    ['Continue your review','Use Search for a known symbol or document. Choose another Explore or Assess view when your question changes. Review results and Analysis limits qualify conclusions. Tutorial replays these lessons for the open view; collection remains an explicit, separate action.','#workspace-tutorial']
  ];
}

function tutorialPosition() {
  if(tutorialDialog.hidden)return;
  const target=document.querySelector(tutorialSteps[tutorialIndex][2]);
  const rect=target?.getBoundingClientRect();
  tutorialSpotlight.hidden=!rect||!rect.width||!rect.height;
  if(!tutorialSpotlight.hidden) {
    Object.assign(tutorialSpotlight.style,{left:Math.max(4,rect.left)+'px',top:Math.max(4,rect.top)+'px',
      width:Math.max(0,Math.min(rect.right,innerWidth-4)-Math.max(4,rect.left))+'px',
      height:Math.max(0,Math.min(rect.bottom,innerHeight-4)-Math.max(4,rect.top))+'px'});
  }
  const width=tutorialDialog.getBoundingClientRect().width,height=tutorialDialog.getBoundingClientRect().height;
  const below=rect&&rect.bottom+height+24<innerHeight;
  const top=below?rect.bottom+12:Math.max(12,innerHeight-height-20);
  tutorialDialog.style.top=top+'px';
  tutorialDialog.style.left=Math.max(12,Math.min(rect?.left||20,innerWidth-width-12))+'px';
}

function tutorialShow() {
  const [title,text]=tutorialSteps[tutorialIndex];
  tutorialTitle.textContent=title;tutorialDescription.textContent=text;
  tutorialProgress.textContent='Step '+(tutorialIndex+1)+' of '+tutorialSteps.length;
  tutorialNotice.textContent=tutorialStorageError;
  tutorialBack.disabled=tutorialIndex===0;
  tutorialNext.textContent=tutorialIndex===tutorialSteps.length-1?'Finish':'Next';
  tutorialPosition();tutorialNext.focus({preventScroll:true});
}

function tutorialStart() {
  tutorialReturnFocus=document.activeElement;
  tutorialSteps=tutorialBuildSteps();
  tutorialIndex=0;tutorialLayer.hidden=false;tutorialDialog.hidden=false;
  tutorialShow();
}

function tutorialClose() {
  tutorialLayer.hidden=true;tutorialDialog.hidden=true;
  try {localStorage.setItem(tutorialKey,'seen');}
  catch(error) {status('Could not remember tutorial dismissal: '+error.message,true);}
  const focus=tutorialReturnFocus?.isConnected&&tutorialReturnFocus!==document.body?tutorialReturnFocus:$('workspace-tutorial');
  focus.focus({preventScroll:true});
}

$('workspace-tutorial').onclick=tutorialStart;
document.addEventListener('keydown',event=>{
  if(tutorialDialog.hidden)return;
  if(event.key==='Escape'){event.preventDefault();event.stopImmediatePropagation();tutorialClose();}
  else if(event.key==='Tab') {
    const buttons=[tutorialSkip,tutorialBack,tutorialNext].filter(b=>!b.disabled);
    const current=buttons.indexOf(document.activeElement);
    event.preventDefault();
    buttons[(current+(event.shiftKey?-1:1)+buttons.length)%buttons.length].focus();
  }
},true);
window.addEventListener('resize',tutorialPosition);
const beforeTutorialRender=render;
render=function() {
  beforeTutorialRender();
  if(!tutorialDialog.hidden) {
    tutorialSteps=tutorialBuildSteps();
    const [title,text]=tutorialSteps[tutorialIndex];
    tutorialTitle.textContent=title;tutorialDescription.textContent=text;
  }
  tutorialPosition();
};
try {if(localStorage.getItem(tutorialKey)!=='seen')tutorialStart();}
catch(error) {
  tutorialStorageError='Browser storage is unavailable; dismissal cannot be remembered: '+error.message;
  tutorialStart();
}
