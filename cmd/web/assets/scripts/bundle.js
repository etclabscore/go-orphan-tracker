(()=>{"use strict";(()=>{var t=document.querySelector("#mytable-body"),n=document.querySelector("#loading-icon"),e=document.querySelector("tr#latest-block");function c(t,n){return'\n<tr class="\n    '.concat(t.orphan?"orphan":"canon"," \n    ").concat("0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347"!==t.sha3Uncles?"uncler":""," \n    ").concat(n?"dupeEnd":"","\n    ").concat(t.is_latest?"latest":"",'\n    ">\n    <td>').concat(t.number,"</td>\n    <td>").concat(t.timestamp,'</td>\n    <td class="truncate-hash">').concat(t.miner,'</td>\n    <td class="truncate-hash">').concat(t.hash,'</td>\n    <td class="truncate-hash">').concat(t.uncleBy,"</td>\n    <td>").concat(t.gasUsed,"</td>\n</tr>")}(function(){if(""===document.location.search&&window.history.replaceState){var t=window.location.protocol+"//"+window.location.host+window.location.pathname+"?limit=100&include_txes=false";window.history.replaceState({path:t},"",t)}return fetch("/api/headers"+window.history.state.search).then((function(t){return t.json()})).catch((function(t){return console.log(t),Promise.reject(t)}))})().then((function(e){n.style.display="none";for(var a=0;a<e.length;a++){var o=e[a],r=c(o,a===e.length-1||e[a+1].number!==o.number);t.innerHTML+=r}})).catch((function(n){return t.innerHTML=n})),fetch("/status").then((function(t){return t.json()})).catch((function(t){return console.log(t),Promise.reject(t)})).then((function(t){t.latest_header.is_latest=!0;var n=c(t.latest_header,!1);if(e.outerHTML)e.outerHTML=n;else{var a=document.createElement("div");a.innerHTML="\x3c!--THIS DATA SHOULD BE REPLACED--\x3e";var o=Obj.parentNode;o.replaceChild(a,e),o.innerHTML=o.innerHTML.replace("<div>\x3c!--THIS DATA SHOULD BE REPLACED--\x3e</div>",n)}})).catch((function(t){}))})()})();