(function(root) {
  'use strict';
  function documentLink(current,href) {
    if(/^[a-z][a-z0-9+.-]*:|^[\\/]|\\/i.test(href))return null;
    const split=href.indexOf('#'),target=split<0?href:href.slice(0,split);
    const fragment=split<0?'':href.slice(split+1);
    let decoded;
    try { decoded=decodeURIComponent(target); } catch { return null; }
    if(/[\\?#]/.test(decoded)||decoded.startsWith('/'))return null;
    const parts=target?current.split('/').slice(0,-1):[];
    for(const part of (target?decoded:current).split('/')) {
      if(part==='..') { if(!parts.length)return null;parts.pop(); }
      else if(part&&part!=='.')parts.push(part);
    }
    return {path:parts.join('/'),fragment};
  }
  function render(container,text) {
    const renderer=new root.marked.Renderer();
    renderer.image=({text})=>{
      const span=root.document.createElement('span');span.textContent=text;
      return span.outerHTML;
    };
    const html=root.marked.parse(text,{gfm:true,renderer,async:false});
    // Restrict Markdown to document structure; no active HTML or asset loads.
    const fragment=root.DOMPurify.sanitize(html,{
      RETURN_DOM_FRAGMENT:true,
      ALLOWED_TAGS:['h1','h2','h3','h4','h5','h6','p','ul','ol','li','blockquote','pre','code',
        'table','thead','tbody','tr','th','td','a','strong','em','del','hr','br','details','summary','span'],
      ALLOWED_ATTR:['href','title','start','colspan','rowspan'],
      ALLOW_DATA_ATTR:false,ALLOW_ARIA_ATTR:false
    });
    for(const link of fragment.querySelectorAll('a[href]')) {
      const href=link.getAttribute('href').trim();
      if(/^https?:\/\/|^mailto:/i.test(href)) {
        link.setAttribute('rel','noopener noreferrer');link.setAttribute('target','_blank');
      } else if(/^[a-z][a-z0-9+.-]*:|^[\\/]|\\/i.test(href))link.removeAttribute('href');
    }
    const slugs=new Map();
    for(const heading of fragment.querySelectorAll('h1,h2,h3,h4,h5,h6')) {
      const slug=heading.textContent.toLowerCase().replace(/[^\p{L}\p{N}_ -]/gu,'').replace(/ /g,'-');
      const count=slugs.get(slug)||0;slugs.set(slug,count+1);
      heading.id='document-heading-'+slug+(count?'-'+count:'');
    }
    container.replaceChildren(fragment);
  }
  root.GoReviewMarkdown={render,documentLink};
})(globalThis);
