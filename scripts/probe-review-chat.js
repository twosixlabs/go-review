// Probe an already-running local workbench without executing target code.
// Args: WORKBENCH_URL PROMPT [navigate]; writes the actual chat event stream.
'use strict';
async function main() {
  const [base,prompt,permission]=process.argv.slice(2),url=new URL(base);
  if(url.protocol!=='http:'||!['127.0.0.1','[::1]'].includes(url.hostname))
    throw Error('A literal HTTP loopback workbench URL is required');
  if(!prompt)throw Error('Prompt is required');
  const stateResponse=await fetch(new URL('/api/chat/status',url));
  const state=await stateResponse.json();
  if(!stateResponse.ok||!state.enabled||!state.snapshot)throw Error(JSON.stringify(state));
  const response=await fetch(new URL('/api/chat',url),{method:'POST',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify({message:prompt,snapshotID:state.snapshot.id,history:[],
      context:{kind:'application',module:state.snapshot.module},allowNavigation:permission==='navigate'}),
    signal:AbortSignal.timeout(300000)});
  if(!response.ok)throw Error(await response.text());
  const reader=response.body.getReader(),decoder=new TextDecoder();
  let pending='',done=false;
  while(true) {
    const part=await reader.read();pending+=decoder.decode(part.value||new Uint8Array(),{stream:!part.done});
    let end;
    while((end=pending.indexOf('\n'))>=0) {
      const line=pending.slice(0,end);pending=pending.slice(end+1);
      if(!line)continue;
      process.stdout.write(line+'\n');
      const event=JSON.parse(line);
      if(event.type==='error')throw Error(event.text);
      if(event.type==='done')done=true;
    }
    if(part.done)break;
  }
  if(!done)throw Error('Chat ended without completion');
}
main().catch(error=>{console.error(error);process.exitCode=1;});
