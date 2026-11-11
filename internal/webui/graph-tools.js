(function(root){
  'use strict';
  function wheelViewport(pan,event,inverse,viewportHeight) {
    const unit=event.deltaMode===1?16:event.deltaMode===2?viewportHeight:1;
    const dx=event.deltaX*unit,dy=event.deltaY*unit;
    // Chromium exposes trackpad pinch as Ctrl+wheel.
    if(event.shiftKey||event.ctrlKey) {
      const factor=Math.exp(Math.max(-200,Math.min(200,dy||dx))*.002);
      return {x:pan.x+pan.w*(1-factor)/2,y:pan.y+pan.h*(1-factor)/2,
        w:pan.w*factor,h:pan.h*factor};
    }
    return {...pan,x:pan.x+inverse.a*dx+inverse.c*dy,
      y:pan.y+inverse.b*dx+inverse.d*dy};
  }
  function intersects(a,b,r) {
    if(a.x===b.x)return a.x>r.x&&a.x<r.x+r.w&&Math.max(a.y,b.y)>r.y&&Math.min(a.y,b.y)<r.y+r.h;
    if(a.y===b.y)return a.y>r.y&&a.y<r.y+r.h&&Math.max(a.x,b.x)>r.x&&Math.min(a.x,b.x)<r.x+r.w;
    return true;
  }
  function route(rectangles,from,to,lane=0,portOffset=0) {
    const a=rectangles.find(r=>r.id===from),b=rectangles.find(r=>r.id===to);
    if(!a||!b)return [];
    const gap=18+(lane%4)*3, dx=b.x+b.w/2-a.x-a.w/2,dy=b.y+b.h/2-a.y-a.h/2;
    let side=Math.abs(dx)>Math.abs(dy)*.8?(dx>=0?'right':'left'):(dy>=0?'bottom':'top');
    const opposite={right:'left',left:'right',top:'bottom',bottom:'top'};
    if(from===to)side='right';
    const port=(r,s)=>{
      const x=r.x+r.w/2+portOffset,y=r.y+r.h/2+portOffset;
      if(s==='right')return [{x:r.x+r.w,y},{x:r.x+r.w+gap,y}];
      if(s==='left')return [{x:r.x,y},{x:r.x-gap,y}];
      if(s==='top')return [{x,y:r.y},{x,y:r.y-gap}];
      return [{x,y:r.y+r.h},{x,y:r.y+r.h+gap}];
    };
    const [start,s]=port(a,side),[end,t]=port(b,from===to?'bottom':opposite[side]);
    const obstacles=rectangles.map(r=>({...r,x:r.x-8,y:r.y-8,w:r.w+16,h:r.h+16}));
    const clear=(p,q)=>!obstacles.some(r=>intersects(p,q,r));
    const valid=points=>points.every((p,i)=>!i||clear(points[i-1],p));
    let middle;
    const candidates=[[s,{x:t.x,y:s.y},t],[s,{x:s.x,y:t.y},t]];
    const xs=[...new Set([s.x,t.x,...rectangles.flatMap(r=>[r.x-gap,r.x+r.w+gap])])].sort((a,b)=>a-b);
    const ys=[...new Set([s.y,t.y,...rectangles.flatMap(r=>[r.y-gap,r.y+r.h+gap])])].sort((a,b)=>a-b);
    for(const x of xs)candidates.push([s,{x,y:s.y},{x,y:t.y},t]);
    for(const y of ys)candidates.push([s,{x:s.x,y},{x:t.x,y},t]);
    const length=points=>points.slice(1).reduce((sum,p,i)=>sum+Math.abs(p.x-points[i].x)+Math.abs(p.y-points[i].y),0);
    candidates.sort((a,b)=>length(a)-length(b));
    middle=candidates.find(valid);
    if(!middle) {
      // Sparse rectilinear visibility grid: every obstacle contributes clearance
      // lanes. A* is only needed when the simpler one-rail routes are obstructed.
      const width=xs.length, startIndex=ys.indexOf(s.y)*width+xs.indexOf(s.x),endIndex=ys.indexOf(t.y)*width+xs.indexOf(t.x);
      const point=index=>({x:xs[index%width],y:ys[Math.floor(index/width)]});
      const costs=new Map([[startIndex,0]]),parents=new Map(),heap=[];
      const push=item=>{heap.push(item);let i=heap.length-1;while(i){const p=(i-1)>>1;if(heap[p].score<=item.score)break;heap[i]=heap[p];i=p;}heap[i]=item;};
      const pop=()=>{const first=heap[0],last=heap.pop();if(heap.length){let i=0;while(i*2+1<heap.length){let c=i*2+1;if(c+1<heap.length&&heap[c+1].score<heap[c].score)c++;if(heap[c].score>=last.score)break;heap[i]=heap[c];i=c;}heap[i]=last;}return first;};
      push({index:startIndex,score:0,cost:0});
      while(heap.length){
        const current=pop(),i=current.index;
        if(current.cost!==costs.get(i))continue;
        if(i===endIndex)break;
        const p=point(i),x=i%width,y=Math.floor(i/width);
        const neighbors=[];
        if(x>0)neighbors.push(i-1);if(x+1<width)neighbors.push(i+1);
        if(y>0)neighbors.push(i-width);if(y+1<ys.length)neighbors.push(i+width);
        for(const n of neighbors){
          const q=point(n);if(!clear(p,q))continue;
          const cost=current.cost+Math.abs(p.x-q.x)+Math.abs(p.y-q.y);
          if(cost>=(costs.get(n)??Infinity))continue;
          costs.set(n,cost);parents.set(n,i);push({index:n,cost,score:cost+Math.abs(q.x-t.x)+Math.abs(q.y-t.y)});
        }
      }
      if(costs.has(endIndex)){
        middle=[];let i=endIndex;
        while(true){middle.push(point(i));if(i===startIndex)break;i=parents.get(i);}
        middle.reverse();
      }
    }
    const points=[start,...(middle||[s,t]),end].filter((p,i,list)=>!i||p.x!==list[i-1].x||p.y!==list[i-1].y);
    points.obstructed=!middle||points.some((p,i)=>i&&rectangles.some(r=>intersects(points[i-1],p,r)));
    return points;
  }
  function bounds(rectangles,points=[]) {
    const xs=rectangles.flatMap(r=>[r.x,r.x+r.w]).concat(points.map(p=>p.x)),ys=rectangles.flatMap(r=>[r.y,r.y+r.h]).concat(points.map(p=>p.y));
    const x=Math.min(0,...xs)-30,y=Math.min(0,...ys)-30;
    return {x,y,w:Math.max(500,Math.max(0,...xs)-x+30),h:Math.max(400,Math.max(0,...ys)-y+30)};
  }
  // Labels need clearance too: an unobstructed line can still put its text
  // behind a neighbouring card. Try both sides of each routed segment.
  function labelPosition(points,width,rectangles,occupied=[],side=-1) {
    const segments=points.slice(1).map((b,i)=>({a:points[i],b}))
      .sort((p,q)=>Math.hypot(q.b.x-q.a.x,q.b.y-q.a.y)-Math.hypot(p.b.x-p.a.x,p.b.y-p.a.y));
    for(const {a,b} of segments)for(const fraction of [.5,.25,.75])for(const sign of [side<0?-1:1,side<0?1:-1]) {
      const vertical=a.x===b.x,cx=a.x+(b.x-a.x)*fraction,cy=a.y+(b.y-a.y)*fraction;
      const box={x:cx-width/2+(vertical?sign*(width/2+10):0),y:cy-7+(vertical?0:sign*18),w:width,h:14};
      if([...rectangles,...occupied].every(r=>
        box.x+box.w+4<=r.x||box.x-4>=r.x+r.w||box.y+box.h+4<=r.y||box.y-4>=r.y+r.h))return box;
    }
    return null;
  }
  const metric=(value,tone,fill)=>({value,tone,fill});
  function coverageMetric(location,coverage) {
    if(!location||!coverage)return metric(null,'unknown',0);
    const cmp=(a,b)=>a[0]-b[0]||a[1]-b[1];
    const locations=Array.isArray(location)?location:[location];
    const blocks=(coverage.blocks||[]).filter(b=>b.statements>0&&locations.some(loc=>b.fileID===loc.fileID&&
      cmp([b.startLine,b.startColumn],[loc.endLine,loc.endColumn])<0&&cmp([b.endLine,b.endColumn],[loc.line,loc.column])>0));
    const total=blocks.reduce((n,b)=>n+b.statements,0);
    if(!total)return metric(null,'unknown',0);
    const value=100*blocks.filter(b=>b.count>0).reduce((n,b)=>n+b.statements,0)/total;
    return {...metric(value,value>=80?'green':value>=60?'amber':value>=40?'orange':'red',value/100),total,covered:blocks.filter(b=>b.count>0).reduce((n,b)=>n+b.statements,0)};
  }
  function complexityMetric(value) {
    if(!Number.isFinite(value)||value<1)return metric(null,'unknown',0);
    return metric(value,value<=5?'green':value<=10?'amber':value<=20?'orange':'red',Math.min(value/25,1));
  }
  function gaugeOptions(kind,value){
    const palette=['#ef4444','#f59b23','#f4cb32','#159447'];
    const stops=kind==='coverage'?[[0,0],[40,1],[60,2],[80,3],[100,3]]:[[1,3],[5,3],[10,2],[20,1],[25,0]];
    const v=Math.max(stops[0][0],Math.min(stops.at(-1)[0],value));
    const i=stops.findIndex((s,j)=>j>0&&v<=s[0]),[a,ha]=stops[i-1],[b,hb]=stops[i];
    const channels=hex=>[1,3,5].map(i=>parseInt(hex.slice(i,i+2),16));
    const from=channels(palette[ha]),to=channels(palette[hb]);
    const color=value===null?'#465367':'#'+from.map((c,i)=>Math.round(c+(to[i]-c)*(v-a)/(b-a)).toString(16).padStart(2,'0')).join('');
    return {
      value:value===null?0:value,min:0,max:kind==='coverage'?100:25,
      width:120,height:60,
      // JustGage's semicircle outer radius is .4w; 8/3 brings its inner radius to zero.
      gaugeWidthScale:8/3,gaugeColor:color,levelColors:[color],noGradient:true,
      hideValue:true,hideMinMax:true,showMinMax:false,showInnerShadow:false,
      startAnimationTime:0,refreshAnimationTime:0,pointer:value!==null,
      pointerOptions:{toplength:-8,bottomlength:0,bottomwidth:4,color:'#ffffff',stroke:'#111827',stroke_width:1.8}
    };
  }
  const api={route,intersects,bounds,labelPosition,coverageMetric,complexityMetric,gaugeOptions,wheelViewport};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoReviewGraph=api;
})(typeof globalThis==='undefined'?this:globalThis);
