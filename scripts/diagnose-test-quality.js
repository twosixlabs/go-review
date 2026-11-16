// Read-only diagnosis of saved Test Quality progress and failure reasons.
// Args: ARCHIVE_DIRECTORY [documents]. Reports load only for document metadata;
// never contacts models or starts work.
'use strict';
const fs=require('node:fs/promises');
const path=require('node:path');

async function main() {
  const [directory,mode]=process.argv.slice(2);
  if(!directory)throw Error('ARCHIVE_DIRECTORY is required');
  if(mode&&mode!=='documents')throw Error('Unknown diagnosis mode');
  const assessments=[];
  for(const entry of await fs.readdir(directory,{withFileTypes:true})) {
    if(!entry.isDirectory()||entry.name.startsWith('.'))continue;
    const file=path.join(directory,entry.name,'assessment.json');
    let archive;
    try {archive=JSON.parse(await fs.readFile(file,'utf8'));}
    catch(error) {if(error.code==='ENOENT')continue;throw error;}
    const metadata=archive.metadata||archive;
    assessments.push({directory:path.dirname(file),archive,metadata});
  }
  assessments.sort((a,b)=>String(b.metadata.createdAt).localeCompare(String(a.metadata.createdAt)));
  console.log(JSON.stringify({assessments:assessments.map(({metadata:a})=>({
    id:a.id,version:a.version,status:a.status,createdAt:a.createdAt,updatedAt:a.updatedAt,
    model:a.model,phase:a.phase,summary:a.summary
  }))},null,2));
  const selected=assessments.find(a=>a.metadata.status==='running')||assessments[0];
  if(!selected)throw Error('No saved assessments found');
  const {metadata:a,archive}=selected;
  if(mode==='documents') {
    for(const ref of archive.reports||[]) {
      if(typeof ref.path!=='string'||path.basename(ref.path)!==ref.path)
        throw Error('Invalid pinned report path');
      const pinned=JSON.parse(await fs.readFile(path.join(selected.directory,ref.path),'utf8'));
      console.log(JSON.stringify({snapshotID:pinned.snapshotID,module:pinned.report.module,
        documents:(pinned.report.documents||[]).map(d=>({id:d.id,path:d.path,characters:Array.from(d.text||'').length})),
        entrySources:(pinned.report.entryPoints?.sources||[]).map(d=>({id:d.id,path:d.path,characters:Array.from(d.text||'').length}))
      },null,2));
    }
  }
  const diagnostics=[];
  for(const ref of archive.diagnostics||[]) {
    if(typeof ref.path!=='string'||path.basename(ref.path)!==ref.path)
      throw Error('Invalid diagnostic record path');
    const raw=await fs.readFile(path.join(selected.directory,ref.path),'utf8');
    const record=JSON.parse(raw);
    diagnostics.push({
      bytes:Buffer.byteLength(raw),
      fields:Object.fromEntries(Object.entries(record).map(([key,value])=>[key,Buffer.byteLength(JSON.stringify(value))])),
      runs:(record.runs||[]).map(run=>({
        fields:Object.fromEntries(Object.entries(run).map(([key,value])=>[key,Buffer.byteLength(JSON.stringify(value))]))
      }))
    });
  }
  const counts={},reasons=new Map(),samples=[];
  for(const ref of archive.items||[]) {
    if(typeof ref.path!=='string'||path.basename(ref.path)!==ref.path)
      throw Error('Invalid item record path');
    const item=JSON.parse(await fs.readFile(path.join(selected.directory,ref.path),'utf8'));
    counts[item.status]=(counts[item.status]||0)+1;
    if(item.reason)reasons.set(item.reason,(reasons.get(item.reason)||0)+1);
    if(item.status==='insufficient-evidence'&&samples.length<3)
      samples.push({name:item.name,package:item.package,reason:item.reason,ratings:item.ratings});
  }
  console.log(JSON.stringify({
    selected:a.id,phase:a.phase,createdAt:a.createdAt,updatedAt:a.updatedAt,
    error:a.error,activity:a.activity,analysis:a.analysis,review:a.review,persistence:a.persistence,
    phaseMillis:a.phaseMillis,policy:a.policy,diagnosticReason:a.diagnosticReason,
    counts,reasons:[...reasons].map(([reason,count])=>({count,reason})),samples,
    diagnostics
  },null,2));
}
main().catch(error=>{console.error(error);process.exitCode=1;});
