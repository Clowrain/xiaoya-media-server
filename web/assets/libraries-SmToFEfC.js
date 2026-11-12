import{c,h as n}from"./index-Dx-YnDQD.js";/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const d=c("HardDrive",[["line",{x1:"22",x2:"2",y1:"12",y2:"12",key:"1y58io"}],["path",{d:"M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z",key:"oot6mr"}],["line",{x1:"6",x2:"6.01",y1:"16",y2:"16",key:"sgf278"}],["line",{x1:"10",x2:"10.01",y1:"16",y2:"16",key:"1l4acy"}]]);/**
 * @license lucide-react v0.468.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const o=c("Plus",[["path",{d:"M5 12h14",key:"1ays0h"}],["path",{d:"M12 5v14",key:"s699le"}]]);async function y(){const a=await n("/admin/libraries");return Array.isArray(a)?a:a.Libraries||[]}async function l(a,i,e){await n("/admin/libraries","POST",{Name:a,Kind:i,Paths:e})}async function m(a,i,e,r){const s={ID:a,Name:i,Kind:e};r&&r.length>0&&(s.Paths=r),await n("/admin/libraries","PUT",s)}async function u(a){await n(`/admin/libraries?id=${encodeURIComponent(a)}`,"DELETE",{ID:a})}async function f(a,i){await n("/admin/library-folders","POST",{ID:a,Path:i})}async function h(a,i){await n("/admin/library-folders","DELETE",{ID:a,Path:i})}async function b(a,i="scan"){await n("/admin/scan","POST",{ID:a,Mode:i})}async function P(){await n("/admin/scan-all","POST",{})}async function L(a){await n(`/admin/cover?id=${encodeURIComponent(a)}&action=generate`,"POST")}function w(a="/media"){return n(`/admin/directories?${new URLSearchParams({Path:a})}`)}function D(){return n("/admin/scan-schedule")}async function S(a){await n("/admin/scan-schedule","PUT",a)}export{d as H,o as P,y as a,D as b,b as c,u as d,l as e,w as f,L as g,f as h,S as i,h as r,P as s,m as u};
