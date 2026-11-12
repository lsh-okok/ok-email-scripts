import{bs as fa,bt as Ze,bu as pa,d as ze,aj as ye,bv as ba,bw as va,r as k,bx as mt,aD as ma,b,aE as ga,o as c,a as _,ao as q,al as We,an as K,aI as At,aF as ya,bf as et,F as de,e as U,b7 as Wt,by as ha,bz as xa,C as pe,bA as wa,aK as u,aJ as V,aL as T,aN as se,aO as ka,bB as Sa,B as X,aQ as It,bC as nt,bD as st,aW as Ca,bE as Ta,a$ as $a,w as Ie,l as ct,bF as Pa,bg as _a,b0 as Ra,bG as gt,ap as yt,b6 as ht,bH as za,O as je,bj as Se,bI as Xe,ah as La,ai as Ba,bJ as Aa,bK as Wa,ba as Ia,b9 as Ce,b5 as Ye,c as Ea,i as s,g as t,a9 as j,f,a2 as he,t as I,h as oe,u as Et,D as Ot,x as Mt,q as A,A as He,Y as Ke,a1 as Ae,m as qe,X as Ut,a6 as Qe,T as dt,p as re,k as ce,a4 as Oa,a7 as Ma,R as Ua,z as ja,S as Ha,a3 as qa,aa as xt}from"./index-B2Zt0Hj1.js";import{C as wt,P as kt}from"./Pagination-CpC3toI2.js";import{D as Je}from"./DataTable-DPZFrIjx.js";import{A as Fa,I as Te}from"./InputNumber-DApTfvRT.js";import{S as St}from"./Switch-D9W6tl60.js";import{_ as ft}from"./_plugin-vue_export-helper-DlAUqK2U.js";import{S as Da}from"./settings-2-CabtH3At.js";var Na=/\s/;function Va(e){for(var o=e.length;o--&&Na.test(e.charAt(o)););return o}var Xa=/^\s+/;function Ya(e){return e&&e.slice(0,Va(e)+1).replace(Xa,"")}var Ct=NaN,Ga=/^[-+]0x[0-9a-f]+$/i,Ka=/^0b[01]+$/i,Qa=/^0o[0-7]+$/i,Ja=parseInt;function Tt(e){if(typeof e=="number")return e;if(fa(e))return Ct;if(Ze(e)){var o=typeof e.valueOf=="function"?e.valueOf():e;e=Ze(o)?o+"":o}if(typeof e!="string")return e===0?e:+e;e=Ya(e);var p=Ka.test(e);return p||Qa.test(e)?Ja(e.slice(2),p?2:8):Ga.test(e)?Ct:+e}var rt=function(){return pa.Date.now()},Za="Expected a function",el=Math.max,tl=Math.min;function al(e,o,p){var z,w,O,P,x,R,S=0,B=!1,M=!1,F=!0;if(typeof e!="function")throw new TypeError(Za);o=Tt(o)||0,Ze(p)&&(B=!!p.leading,M="maxWait"in p,O=M?el(Tt(p.maxWait)||0,o):O,F="trailing"in p?!!p.trailing:F);function Z(m){var r=z,J=w;return z=w=void 0,S=m,P=e.apply(J,r),P}function $(m){return S=m,x=setTimeout(h,o),B?Z(m):P}function W(m){var r=m-R,J=m-S,Y=o-r;return M?tl(Y,O-J):Y}function Q(m){var r=m-R,J=m-S;return R===void 0||r>=o||r<0||M&&J>=O}function h(){var m=rt();if(Q(m))return ee(m);x=setTimeout(h,W(m))}function ee(m){return x=void 0,F&&z?Z(m):(z=w=void 0,P)}function ae(){x!==void 0&&clearTimeout(x),S=0,z=R=w=x=void 0}function le(){return x===void 0?P:ee(rt())}function H(){var m=rt(),r=Q(m);if(z=arguments,w=this,R=m,r){if(x===void 0)return $(R);if(M)return clearTimeout(x),x=setTimeout(h,o),Z(R)}return x===void 0&&(x=setTimeout(h,o)),P}return H.cancel=ae,H.flush=le,H}var ll="Expected a function";function nl(e,o,p){var z=!0,w=!0;if(typeof e!="function")throw new TypeError(ll);return Ze(p)&&(z="leading"in p?!!p.leading:z,w="trailing"in p?!!p.trailing:w),al(e,o,{leading:z,maxWait:o,trailing:w})}const sl=mt(".v-x-scroll",{overflow:"auto",scrollbarWidth:"none"},[mt("&::-webkit-scrollbar",{width:0,height:0})]),rl=ze({name:"XScroll",props:{disabled:Boolean,onScroll:Function},setup(){const e=k(null);function o(w){!(w.currentTarget.offsetWidth<w.currentTarget.scrollWidth)||w.deltaY===0||(w.currentTarget.scrollLeft+=w.deltaY+w.deltaX,w.preventDefault())}const p=ba();return sl.mount({id:"vueuc/x-scroll",head:!0,anchorMetaName:va,ssr:p}),Object.assign({selfRef:e,handleWheel:o},{scrollTo(...w){var O;(O=e.value)===null||O===void 0||O.scrollTo(...w)}})},render(){return ye("div",{ref:"selfRef",onScroll:this.onScroll,onWheel:this.disabled?void 0:this.handleWheel,class:"v-x-scroll"},this.$slots)}});var il=ze({name:"ChevronLeft",render(){return(()=>{const e=ma("dfe229c2639b2082");return e[0]||(e[0]=b("svg",{viewBox:"0 0 16 16",fill:"none",xmlns:"http://www.w3.org/2000/svg"},[b("path",{d:"M10.3536 3.14645C10.5488 3.34171 10.5488 3.65829 10.3536 3.85355L6.20711 8L10.3536 12.1464C10.5488 12.3417 10.5488 12.6583 10.3536 12.8536C10.1583 13.0488 9.84171 13.0488 9.64645 12.8536L5.14645 8.35355C4.95118 8.15829 4.95118 7.84171 5.14645 7.64645L9.64645 3.14645C9.84171 2.95118 10.1583 2.95118 10.3536 3.14645Z",fill:"currentColor"})],-1))})()}});const pt=ga("n-tabs"),jt={tab:[String,Number,Object,Function],name:{type:[String,Number],required:!0},disabled:Boolean,displayDirective:{type:String,default:"if"},closable:{type:Boolean,default:void 0},tabProps:Object,label:[String,Number,Object,Function]};var $t=ze({__TAB_PANE__:!0,name:"TabPane",alias:["TabPanel"],props:jt,slots:Object,setup(e){const o=At(pt,null);return o||ya("tab-pane","`n-tab-pane` must be placed inside `n-tabs`."),{style:o.paneStyleRef,class:o.paneClassRef,mergedClsPrefix:o.mergedClsPrefixRef}},render(){return c(),_("div",{class:K([`${this.mergedClsPrefix}-tab-pane`,this.class]),style:We(this.style)},[q(()=>this.$slots.default?.())],6)}});const ol=["data-name","data-disabled"],dl={internalLeftPadded:Boolean,internalAddable:Boolean,internalCreatedByPane:Boolean,...wa(jt,["displayDirective"])};var ut=ze({__TAB__:!0,inheritAttrs:!1,name:"Tab",props:dl,setup(e){const{mergedClsPrefixRef:o,valueRef:p,typeRef:z,closableRef:w,tabStyleRef:O,addTabStyleRef:P,tabClassRef:x,addTabClassRef:R,tabChangeIdRef:S,onBeforeLeaveRef:B,triggerRef:M,handleAdd:F,activateTab:Z,handleClose:$}=At(pt);return{trigger:M,mergedClosable:pe(()=>{if(e.internalAddable)return!1;const{closable:W}=e;return W===void 0?w.value:W}),style:O,addStyle:P,tabClass:x,addTabClass:R,clsPrefix:o,value:p,type:z,handleClose(W){W.stopPropagation(),!e.disabled&&$(e.name)},activateTab(){if(e.disabled)return;if(e.internalAddable){F();return}const{name:W}=e,Q=++S.id;if(W!==p.value){const{value:h}=B;h?Promise.resolve(h(e.name,p.value)).then(ee=>{ee&&S.id===Q&&Z(W)}):Z(W)}}}},render(){const{internalAddable:e,clsPrefix:o,name:p,disabled:z,label:w,tab:O,value:P,mergedClosable:x,trigger:R,$slots:{default:S}}=this,B=w??O;return c(),_("div",{class:K(`${o}-tabs-tab-wrapper`)},[this.internalLeftPadded?(c(),_("div",{key:0,class:K(`${o}-tabs-tab-pad`)},null,2)):q(()=>null),(c(),_("div",et({key:p,"data-name":p,"data-disabled":z?!0:void 0},et({class:[`${o}-tabs-tab`,P===p&&`${o}-tabs-tab--active`,z&&`${o}-tabs-tab--disabled`,x&&`${o}-tabs-tab--closable`,e&&`${o}-tabs-tab--addable`,e?this.addTabClass:this.tabClass],onClick:R==="click"?this.activateTab:void 0,onMouseenter:R==="hover"?this.activateTab:void 0,style:e?this.addStyle:this.style},this.internalCreatedByPane?this.tabProps||{}:this.$attrs)),[b("span",{class:K(`${o}-tabs-tab__label`)},[e?(c(),_(de,{key:0},[b("div",{class:K(`${o}-tabs-tab__height-placeholder`)}," ",2),(c(),U(Wt,{clsPrefix:o},{default:()=>(c(),U(Fa))},1032,["clsPrefix"]))],64)):(c(),_(de,{key:1},[S?(c(),_(de,{key:0},[q(()=>S())],64)):(c(),_(de,{key:1},[typeof B=="object"?(c(),_(de,{key:0},[q(()=>B)],64)):(c(),_(de,{key:1},[q(()=>ha(B??p))],64))],64))],64))],2),x&&this.type==="card"?(c(),U(xa,{key:0,clsPrefix:o,class:K(`${o}-tabs-tab__close`),onClick:this.handleClose,disabled:z},null,8,["clsPrefix","class","onClick","disabled"])):q(()=>null)],16,ol))],2)}}),ul=u("tabs",`
 box-sizing: border-box;
 width: 100%;
 display: flex;
 flex-direction: column;
 transition:
 background-color .3s var(--n-bezier),
 border-color .3s var(--n-bezier);
`,[V("&.transition-disabled",[u("tabs-tab",`
 transition: none !important;
 `),u("tabs-nav-scroll-content",`
 transition: none !important;
 `),u("tabs-tab-pad",`
 transition: none !important;
 `)]),T("segment-type",[u("tabs-rail",[V("&.transition-disabled",[u("tabs-capsule",`
 transition: none;
 `)])])]),T("top",[u("tab-pane",`
 padding: var(--n-pane-padding-top) var(--n-pane-padding-right) var(--n-pane-padding-bottom) var(--n-pane-padding-left);
 `)]),T("left",[u("tab-pane",`
 padding: var(--n-pane-padding-right) var(--n-pane-padding-bottom) var(--n-pane-padding-left) var(--n-pane-padding-top);
 `)]),T("left, right",`
 flex-direction: row;
 `,[u("tabs-bar",`
 width: 2px;
 right: 0;
 transition:
 top .2s var(--n-bezier),
 max-height .2s var(--n-bezier),
 background-color .3s var(--n-bezier);
 `),u("tabs-tab",`
 padding: var(--n-tab-padding-vertical); 
 `)]),T("right",`
 flex-direction: row-reverse;
 `,[u("tab-pane",`
 padding: var(--n-pane-padding-left) var(--n-pane-padding-top) var(--n-pane-padding-right) var(--n-pane-padding-bottom);
 `),u("tabs-bar",`
 left: 0;
 `)]),T("bottom",`
 flex-direction: column-reverse;
 justify-content: flex-end;
 `,[u("tab-pane",`
 padding: var(--n-pane-padding-bottom) var(--n-pane-padding-right) var(--n-pane-padding-top) var(--n-pane-padding-left);
 `),u("tabs-bar",`
 top: 0;
 `)]),u("tabs-rail",`
 position: relative;
 padding: 3px;
 border-radius: var(--n-tab-border-radius);
 width: 100%;
 background-color: var(--n-color-segment);
 transition: background-color .3s var(--n-bezier);
 display: flex;
 align-items: center;
 `,[u("tabs-capsule",`
 border-radius: var(--n-tab-border-radius);
 position: absolute;
 left: 0;
 top: 0;
 pointer-events: none;
 background-color: var(--n-tab-color-segment);
 box-shadow: 0 1px 3px 0 rgba(0, 0, 0, .08);
 transition: transform 0.3s var(--n-bezier);
 `),u("tabs-tab-wrapper",`
 flex-basis: 0;
 flex-grow: 1;
 display: flex;
 align-items: center;
 justify-content: center;
 `,[u("tabs-tab",`
 overflow: hidden;
 border-radius: var(--n-tab-border-radius);
 width: 100%;
 display: flex;
 align-items: center;
 justify-content: center;
 `,[T("active",`
 font-weight: var(--n-font-weight-strong);
 color: var(--n-tab-text-color-active);
 `),V("&:hover",`
 color: var(--n-tab-text-color-hover);
 `)])])]),T("flex",[u("tabs-nav",`
 width: 100%;
 position: relative;
 `,[u("tabs-wrapper",`
 width: 100%;
 `,[u("tabs-tab",`
 margin-right: 0;
 `)])])]),u("tabs-nav",`
 box-sizing: border-box;
 line-height: 1.5;
 display: flex;
 transition: border-color .3s var(--n-bezier);
 `,[se("prefix, suffix",`
 display: flex;
 align-items: center;
 `),se("prefix","padding-right: 16px;"),se("suffix","padding-left: 16px;")]),T("top, bottom",[V(">",[u("tabs-nav",[u("tabs-nav-scroll-wrapper",[V("&::before",`
 top: 0;
 bottom: 0;
 left: 0;
 width: 20px;
 `),V("&::after",`
 top: 0;
 bottom: 0;
 right: 0;
 width: 20px;
 `),T("shadow-start",[V("&::before",`
 box-shadow: inset 10px 0 8px -8px rgba(0, 0, 0, .12);
 `)]),T("shadow-end",[V("&::after",`
 box-shadow: inset -10px 0 8px -8px rgba(0, 0, 0, .12);
 `)])])])])]),T("left, right",[u("tabs-nav-scroll-content",`
 flex-direction: column;
 `),V(">",[u("tabs-nav",[u("tabs-nav-scroll-wrapper",[V("&::before",`
 top: 0;
 left: 0;
 right: 0;
 height: 20px;
 `),V("&::after",`
 bottom: 0;
 left: 0;
 right: 0;
 height: 20px;
 `),T("shadow-start",[V("&::before",`
 box-shadow: inset 0 10px 8px -8px rgba(0, 0, 0, .12);
 `)]),T("shadow-end",[V("&::after",`
 box-shadow: inset 0 -10px 8px -8px rgba(0, 0, 0, .12);
 `)])])])])]),u("tabs-nav-scroll-wrapper",`
 flex: 1;
 position: relative;
 overflow: hidden;
 `,[u("tabs-nav-y-scroll",`
 height: 100%;
 width: 100%;
 overflow-y: auto; 
 scrollbar-width: none;
 `,[V("&::-webkit-scrollbar, &::-webkit-scrollbar-track-piece, &::-webkit-scrollbar-thumb",`
 width: 0;
 height: 0;
 display: none;
 `)]),V("&::before, &::after",`
 transition: box-shadow .3s var(--n-bezier);
 pointer-events: none;
 content: "";
 position: absolute;
 z-index: 1;
 `),V("&.transition-disabled",[V("&::before, &::after",`
 transition: none;
 `)])]),u("tabs-nav-scroll-content",`
 display: flex;
 position: relative;
 min-width: 100%;
 min-height: 100%;
 width: fit-content;
 box-sizing: border-box;
 `),u("tabs-wrapper",`
 display: inline-flex;
 flex-wrap: nowrap;
 position: relative;
 `),u("tabs-tab-wrapper",`
 display: flex;
 flex-wrap: nowrap;
 flex-shrink: 0;
 flex-grow: 0;
 `),u("tabs-tab",`
 cursor: pointer;
 white-space: nowrap;
 flex-wrap: nowrap;
 display: inline-flex;
 align-items: center;
 color: var(--n-tab-text-color);
 font-size: var(--n-tab-font-size);
 background-clip: padding-box;
 padding: var(--n-tab-padding);
 transition:
 box-shadow .3s var(--n-bezier),
 color .3s var(--n-bezier),
 background-color .3s var(--n-bezier),
 border-color .3s var(--n-bezier);
 `,[T("disabled",{cursor:"not-allowed"}),se("close",`
 margin-inline-start: 6px;
 transition:
 background-color .3s var(--n-bezier),
 color .3s var(--n-bezier);
 `),se("label",`
 display: flex;
 align-items: center;
 z-index: 1;
 `)]),u("tabs-bar",`
 position: absolute;
 bottom: 0;
 height: 2px;
 border-radius: 1px;
 background-color: var(--n-bar-color);
 transition:
 left .2s var(--n-bezier),
 max-width .2s var(--n-bezier),
 opacity .3s var(--n-bezier),
 background-color .3s var(--n-bezier);
 `,[V("&.transition-disabled",`
 transition: none;
 `),T("disabled",`
 background-color: var(--n-tab-text-color-disabled)
 `)]),u("tabs-pane-wrapper",`
 position: relative;
 overflow: hidden;
 transition: max-height .2s var(--n-bezier);
 `),u("tab-pane",`
 color: var(--n-pane-text-color);
 width: 100%;
 transition:
 color .3s var(--n-bezier),
 background-color .3s var(--n-bezier),
 opacity .2s var(--n-bezier);
 left: 0;
 right: 0;
 top: 0;
 `,[V("&.next-transition-leave-active, &.prev-transition-leave-active, &.next-transition-enter-active, &.prev-transition-enter-active",`
 transition:
 color .3s var(--n-bezier),
 background-color .3s var(--n-bezier),
 transform .2s var(--n-bezier),
 opacity .2s var(--n-bezier);
 `),V("&.next-transition-leave-active, &.prev-transition-leave-active",`
 position: absolute;
 `),V("&.next-transition-enter-from, &.prev-transition-leave-to",`
 transform: translateX(32px);
 opacity: 0;
 `),V("&.next-transition-leave-to, &.prev-transition-enter-from",`
 transform: translateX(-32px);
 opacity: 0;
 `),V("&.next-transition-leave-from, &.next-transition-enter-to, &.prev-transition-leave-from, &.prev-transition-enter-to",`
 transform: translateX(0);
 opacity: 1;
 `)]),u("tabs-tab-pad",`
 box-sizing: border-box;
 width: var(--n-tab-gap);
 flex-grow: 0;
 flex-shrink: 0;
 `),T("line-type, bar-type",[u("tabs-tab",`
 font-weight: var(--n-tab-font-weight);
 box-sizing: border-box;
 vertical-align: bottom;
 `,[V("&:hover",{color:"var(--n-tab-text-color-hover)"}),T("active",`
 color: var(--n-tab-text-color-active);
 font-weight: var(--n-tab-font-weight-active);
 `),T("disabled",{color:"var(--n-tab-text-color-disabled)"})])]),u("tabs-nav",[se("prefix, suffix",`
 border-color: var(--n-tab-border-color);
 `),u("tabs-nav-scroll-content",`
 border-color: var(--n-tab-border-color);
 `),T("line-type",[T("top",[se("prefix, suffix",`
 border-bottom: 1px solid var(--n-tab-border-color);
 `),u("tabs-nav-scroll-content",`
 border-bottom: 1px solid var(--n-tab-border-color);
 `),u("tabs-bar",`
 bottom: -1px;
 `)]),T("left",[se("prefix, suffix",`
 border-right: 1px solid var(--n-tab-border-color);
 `),u("tabs-nav-scroll-content",`
 border-right: 1px solid var(--n-tab-border-color);
 `),u("tabs-bar",`
 right: -1px;
 `)]),T("right",[se("prefix, suffix",`
 border-left: 1px solid var(--n-tab-border-color);
 `),u("tabs-nav-scroll-content",`
 border-left: 1px solid var(--n-tab-border-color);
 `),u("tabs-bar",`
 left: -1px;
 `)]),T("bottom",[se("prefix, suffix",`
 border-top: 1px solid var(--n-tab-border-color);
 `),u("tabs-nav-scroll-content",`
 border-top: 1px solid var(--n-tab-border-color);
 `),u("tabs-bar",`
 top: -1px;
 `)]),se("prefix, suffix",`
 transition: border-color .3s var(--n-bezier);
 `),u("tabs-nav-scroll-content",`
 transition: border-color .3s var(--n-bezier);
 `),u("tabs-bar",`
 border-radius: 0;
 `)]),T("card-type",[se("prefix, suffix",`
 transition: border-color .3s var(--n-bezier);
 `),u("tabs-pad",`
 flex-grow: 1;
 transition: border-color .3s var(--n-bezier);
 `),u("tabs-tab-pad",`
 transition: border-color .3s var(--n-bezier);
 `),u("tabs-tab",`
 font-weight: var(--n-tab-font-weight);
 border: 1px solid var(--n-tab-border-color);
 background-color: var(--n-tab-color);
 box-sizing: border-box;
 position: relative;
 vertical-align: bottom;
 display: flex;
 justify-content: space-between;
 font-size: var(--n-tab-font-size);
 color: var(--n-tab-text-color);
 `,[T("addable",`
 padding-left: 8px;
 padding-right: 8px;
 font-size: 16px;
 justify-content: center;
 `,[se("height-placeholder",`
 width: 0;
 font-size: var(--n-tab-font-size);
 `),ka("disabled",[V("&:hover",`
 color: var(--n-tab-text-color-hover);
 `)])]),T("closable","padding-inline-end: 8px;"),T("active",`
 background-color: #0000;
 font-weight: var(--n-tab-font-weight-active);
 color: var(--n-tab-text-color-active);
 `),T("disabled","color: var(--n-tab-text-color-disabled);")])]),T("left, right",`
 flex-direction: column; 
 `,[se("prefix, suffix",`
 padding: var(--n-tab-padding-vertical);
 `),u("tabs-wrapper",`
 flex-direction: column;
 `),u("tabs-tab-wrapper",`
 flex-direction: column;
 `,[u("tabs-tab-pad",`
 height: var(--n-tab-gap-vertical);
 width: 100%;
 `)])]),T("top",[T("card-type",[u("tabs-scroll-padding","border-bottom: 1px solid var(--n-tab-border-color);"),se("prefix, suffix",`
 border-bottom: 1px solid var(--n-tab-border-color);
 `),u("tabs-tab",`
 border-top-left-radius: var(--n-tab-border-radius);
 border-top-right-radius: var(--n-tab-border-radius);
 `,[T("active",`
 border-bottom: 1px solid #0000;
 `)]),u("tabs-tab-pad",`
 border-bottom: 1px solid var(--n-tab-border-color);
 `),u("tabs-pad",`
 border-bottom: 1px solid var(--n-tab-border-color);
 `)])]),T("left",[T("card-type",[u("tabs-scroll-padding","border-right: 1px solid var(--n-tab-border-color);"),se("prefix, suffix",`
 border-right: 1px solid var(--n-tab-border-color);
 `),u("tabs-tab",`
 border-top-left-radius: var(--n-tab-border-radius);
 border-bottom-left-radius: var(--n-tab-border-radius);
 `,[T("active",`
 border-right: 1px solid #0000;
 `)]),u("tabs-tab-pad",`
 border-right: 1px solid var(--n-tab-border-color);
 `),u("tabs-pad",`
 border-right: 1px solid var(--n-tab-border-color);
 `)])]),T("right",[T("card-type",[u("tabs-scroll-padding","border-left: 1px solid var(--n-tab-border-color);"),se("prefix, suffix",`
 border-left: 1px solid var(--n-tab-border-color);
 `),u("tabs-tab",`
 border-top-right-radius: var(--n-tab-border-radius);
 border-bottom-right-radius: var(--n-tab-border-radius);
 `,[T("active",`
 border-left: 1px solid #0000;
 `)]),u("tabs-tab-pad",`
 border-left: 1px solid var(--n-tab-border-color);
 `),u("tabs-pad",`
 border-left: 1px solid var(--n-tab-border-color);
 `)])]),T("bottom",[T("card-type",[u("tabs-scroll-padding","border-top: 1px solid var(--n-tab-border-color);"),se("prefix, suffix",`
 border-top: 1px solid var(--n-tab-border-color);
 `),u("tabs-tab",`
 border-bottom-left-radius: var(--n-tab-border-radius);
 border-bottom-right-radius: var(--n-tab-border-radius);
 `,[T("active",`
 border-top: 1px solid #0000;
 `)]),u("tabs-tab-pad",`
 border-top: 1px solid var(--n-tab-border-color);
 `),u("tabs-pad",`
 border-top: 1px solid var(--n-tab-border-color);
 `)])])]),u("tabs-scroll-button",[T("start",`
 padding-left: 10px;
 padding-right: 6px;
 `),T("end",`
 padding-right: 10px;
 padding-left: 6px;
 `),T("up",`
 padding-bottom: 10px;
 `),T("down",`
 padding-top: 10px;
 `)])]),Pt=ze({name:"TabsButton",props:{type:{type:String,default:"next"},mergedClsPrefix:{type:String,required:!0},vertical:Boolean,disabled:Boolean,rtl:Boolean,theme:Object,themeOverrides:Object,onClick:Function},setup(e){return{handleClick:()=>{e.disabled||e.onClick?.(e.type)}}},render(){const{mergedClsPrefix:e,disabled:o,type:p,vertical:z,rtl:w,theme:O,themeOverrides:P,handleClick:x}=this,R=p==="next",S=z?R:w?!R:R;return c(),U(X,{text:!0,disabled:o,size:"small",theme:O,themeOverrides:P,onClick:x,class:K([`${e}-tabs-scroll-button`,!z&&p==="prev"&&`${e}-tabs-scroll-button--start`,!z&&p==="next"&&`${e}-tabs-scroll-button--end`,z&&p==="prev"&&`${e}-tabs-scroll-button--up`,z&&p==="next"&&`${e}-tabs-scroll-button--down`])},{icon:()=>(c(),U(Wt,{clsPrefix:e,style:We(z?{transform:"rotate(90deg)"}:void 0)},{default:()=>S?(c(),U(Sa,{key:1})):(c(),U(il,{key:2}))},1032,["clsPrefix","style"]))},1032,["disabled","theme","themeOverrides","onClick","class"])}});const it=nl,cl={...It.props,value:[String,Number],defaultValue:[String,Number],trigger:{type:String,default:"click"},type:{type:String,default:"bar"},closable:Boolean,justifyContent:String,size:String,placement:{type:String,default:"top"},tabStyle:[String,Object],tabClass:String,addTabStyle:[String,Object],addTabClass:String,barWidth:Number,paneClass:String,paneStyle:[String,Object],paneWrapperClass:String,paneWrapperStyle:[String,Object],addable:[Boolean,Object],tabsPadding:{type:Number,default:0},animated:Boolean,onBeforeLeave:Function,onAdd:Function,"onUpdate:value":[Function,Array],onUpdateValue:[Function,Array],onClose:[Function,Array],labelSize:String,activeName:[String,Number],onActiveNameChange:[Function,Array],showScrollButton:Boolean,centerActiveTab:Boolean};var fl=ze({name:"Tabs",props:cl,slots:Object,setup(e,{slots:o}){const{mergedClsPrefixRef:p,inlineThemeDisabled:z,mergedComponentPropsRef:w,mergedRtlRef:O}=Ca(e),P=Ta("Tabs",O,p),x=pe(()=>{const{placement:l}=e;return l==="start"?P?.value?"right":"left":l==="end"?P?.value?"left":"right":l}),R=It("Tabs","-tabs",ul,za,e,p),S=k(null),B=k(null),M=k(null),F=k(null),Z=k(null),$=k(null),W=k(null),Q=k(!0),h=k(!0),ee=gt(e,["labelSize","size"]),ae=pe(()=>{if(ee.value)return ee.value;const l=w?.value?.Tabs?.size;return l||"medium"}),le=gt(e,["activeName","value"]),H=k(le.value??e.defaultValue??(o.default?nt(o.default())[0]?.props?.name:null)),m=$a(le,H),r={id:0},J=pe(()=>{if(!(!e.justifyContent||e.type==="card"))return{display:"flex",justifyContent:e.justifyContent}});Ie(m,()=>{r.id=0,D(),je(()=>{xe()})});function Y(){const{value:l}=m;return l===null?null:S.value?.querySelector(`[data-name="${l}"]`)}function be(l){if(e.type==="card")return;const{value:i}=M;if(!i)return;const g=i.style.opacity==="0";if(l){const L=`${p.value}-tabs-bar--disabled`,{barWidth:N}=e,te=x.value;if(l.dataset.disabled==="true"?i.classList.add(L):i.classList.remove(L),["top","bottom"].includes(te)){if(E(["top","maxHeight","height"]),typeof N=="number"&&l.offsetWidth>=N){const G=Math.floor((l.offsetWidth-N)/2)+l.offsetLeft;i.style.left=`${G}px`,i.style.maxWidth=`${N}px`}else i.style.left=`${l.offsetLeft}px`,i.style.maxWidth=`${l.offsetWidth}px`;i.style.width="8192px",g&&(i.style.transition="none"),i.offsetWidth,g&&(i.style.transition="",i.style.opacity="1")}else{if(E(["left","maxWidth","width"]),typeof N=="number"&&l.offsetHeight>=N){const G=Math.floor((l.offsetHeight-N)/2)+l.offsetTop;i.style.top=`${G}px`,i.style.maxHeight=`${N}px`}else i.style.top=`${l.offsetTop}px`,i.style.maxHeight=`${l.offsetHeight}px`;i.style.height="8192px",g&&(i.style.transition="none"),i.offsetHeight,g&&(i.style.transition="",i.style.opacity="1")}}}function ie(){if(e.type==="card")return;const{value:l}=M;l&&(l.style.opacity="0")}function E(l){const{value:i}=M;if(i)for(const g of l)i.style[g]=""}function D(){if(e.type==="card")return;const l=Y();l?be(l):ie()}function ve(l,i,g,L){const N=l.getBoundingClientRect(),te=i.getBoundingClientRect(),G=g?"left":"top",ne=g?"right":"bottom";let ke=0;L?ke=(te[G]+te[ne])/2-(N[G]+N[ne])/2:te[G]<N[G]?ke=te[G]-N[G]:te[ne]>N[ne]&&(ke=te[ne]-N[ne]),ke!==0&&l.scrollBy({[G]:ke,behavior:"smooth"})}function xe(){const l=["top","bottom"].includes(x.value),i=Y();if(i)if(l){const g=$.value?.$el;if(!g)return;ve(g,i,l,e.centerActiveTab)}else{const{value:g}=W;if(!g)return;ve(g,i,l,e.centerActiveTab)}}const we=k(null);let me=0,ue=null;function Oe(l){const i=we.value;if(i){me=l.getBoundingClientRect().height;const g=`${me}px`,L=()=>{i.style.height=g,i.style.maxHeight=g};ue?(L(),ue(),ue=null):ue=L}}function C(l){const i=we.value;if(i){const g=l.getBoundingClientRect().height,L=()=>{document.body.offsetHeight,i.style.maxHeight=`${g}px`,i.style.height=`${Math.max(me,g)}px`};ue?(ue(),ue=null,L()):ue=L}}function a(){const l=we.value;if(l){l.style.maxHeight="",l.style.height="";const{paneWrapperStyle:i}=e;if(typeof i=="string")l.style.cssText=i;else if(i){const{maxHeight:g,height:L}=i;g!==void 0&&(l.style.maxHeight=g),L!==void 0&&(l.style.height=L)}}}const v={value:[]},ge=k("next");function Be(l){const i=m.value;let g="next";for(const L of v.value){if(L===i)break;if(L===l){g="prev";break}}ge.value=g,$e(l)}function $e(l){const{onActiveNameChange:i,onUpdateValue:g,"onUpdate:value":L}=e;i&&Ye(i,l),g&&Ye(g,l),L&&Ye(L,l),H.value=l}function tt(l){const{onClose:i}=e;i&&Ye(i,l)}function Fe(l){if(["top","bottom"].includes(x.value)){const{value:i}=$;if(!i)return;const g=i.$el;if(!g)return;const L=g.offsetWidth,N=!!P?.value,te=l==="next"?L:-L;g.scrollBy({left:N?-te:te,behavior:"smooth"})}else{const{value:i}=W;if(!i)return;const g=i.offsetHeight,L=l==="next"?i.scrollTop+g:i.scrollTop-g;i.scrollTo({top:L,left:0,behavior:"smooth"})}}let Ee=!0;function Me(){const{value:l}=M;if(!l)return;Ee&&(Ee=!1);const i="transition-disabled";l.classList.add(i),D(),l.classList.remove(i)}const Pe=k(null);function Le({transitionDisabled:l}){const i=S.value;if(!i)return;l&&i.classList.add("transition-disabled");const g=Y();g&&Pe.value&&(Pe.value.style.width=`${g.offsetWidth}px`,Pe.value.style.height=`${g.offsetHeight}px`,Pe.value.style.transform=`translate(${g.offsetLeft}px, ${g.offsetTop}px)`,l&&Pe.value.offsetWidth),l&&i.classList.remove("transition-disabled")}Ie([m],()=>{e.type==="segment"&&je(()=>{Le({transitionDisabled:!1})})}),ct(()=>{e.type==="segment"&&Le({transitionDisabled:!0})});let _e=0;function at(l){if(l.contentRect.width===0&&l.contentRect.height===0||_e===l.contentRect.width)return;_e=l.contentRect.width;const{type:i}=e;(i==="line"||i==="bar")&&(Ee||e.justifyContent?.startsWith("space"))&&Me(),i!=="segment"&&Ne(fe())}const De=it(at,64);function Ue(){const{type:l}=e;l==="line"||l==="bar"?Me():l==="segment"&&Le({transitionDisabled:!0})}Ie([()=>e.justifyContent,()=>e.size],()=>{je(()=>{(e.type==="line"||e.type==="bar")&&Me()})}),Ie([x,()=>P?.value],()=>{je(()=>{Ue(),Ne(fe(),{instantly:!0})})}),Ie(()=>e.type,()=>{je(()=>{const l=B.value;l&&(l.classList.add("transition-disabled"),Ue(),l.offsetWidth,l.classList.remove("transition-disabled"))})});const Re=k(!1);function lt(l){const{target:i,contentRect:{width:g,height:L}}=l,N=i.parentElement.parentElement.offsetWidth,te=i.parentElement.parentElement.offsetHeight,G=x.value;if(!Re.value)G==="top"||G==="bottom"?N<g&&(Re.value=!0):te<L&&(Re.value=!0);else{const{value:ne}=Z;if(!ne)return;G==="top"||G==="bottom"?N-g>ne.$el.offsetWidth&&(Re.value=!1):te-L>ne.$el.offsetHeight&&(Re.value=!1)}Ne($.value?.$el||null)}const d=it(lt,64);function n(){const{onAdd:l}=e;l&&l()}const y=k(!1);function fe(){const l=x.value;return(l==="top"||l==="bottom"?$.value?.$el:W.value)||null}function Ne(l,i={instantly:!1}){if(!l)return;const g=i.instantly?F.value:null;g&&g.classList.add("transition-disabled");const L=1,N=x.value;if(N==="top"||N==="bottom"){const{scrollLeft:te,scrollWidth:G,offsetWidth:ne}=l,ke=Math.abs(te);Q.value=ke<=L,h.value=ke+ne>=G-L,y.value=ne<G-L}else{const{scrollTop:te,scrollHeight:G,offsetHeight:ne}=l;Q.value=te<=L,h.value=te+ne>=G-L,y.value=ne<G-L}g&&(g.offsetWidth,g.classList.remove("transition-disabled"))}const Ht=it(l=>{Ne(l.target)},64);Ia(pt,{triggerRef:Ce(e,"trigger"),tabStyleRef:Ce(e,"tabStyle"),tabClassRef:Ce(e,"tabClass"),addTabStyleRef:Ce(e,"addTabStyle"),addTabClassRef:Ce(e,"addTabClass"),paneClassRef:Ce(e,"paneClass"),paneStyleRef:Ce(e,"paneStyle"),mergedClsPrefixRef:p,typeRef:Ce(e,"type"),closableRef:Ce(e,"closable"),valueRef:m,tabChangeIdRef:r,onBeforeLeaveRef:Ce(e,"onBeforeLeave"),activateTab:Be,handleClose:tt,handleAdd:n}),Pa(()=>{D(),xe()}),_a(()=>{const{value:l}=F;if(!l)return;const{value:i}=p,g=`${i}-tabs-nav-scroll-wrapper--shadow-start`,L=`${i}-tabs-nav-scroll-wrapper--shadow-end`;Q.value?l.classList.remove(g):l.classList.add(g),h.value?l.classList.remove(L):l.classList.add(L)});const qt={syncBarPosition:()=>{D()},scrollToCurrentTab:()=>{xe()}},Ft=()=>{Le({transitionDisabled:!0})},bt=pe(()=>{const{value:l}=ae,{type:i}=e,g=`${l}${{card:"Card",bar:"Bar",line:"Line",segment:"Segment"}[i]}`,{self:{barColor:L,closeIconColor:N,closeIconColorHover:te,closeIconColorPressed:G,tabColor:ne,tabBorderColor:ke,paneTextColor:Dt,tabFontWeight:Nt,tabBorderRadius:Vt,tabFontWeightActive:Xt,colorSegment:Yt,fontWeightStrong:Gt,tabColorSegment:Kt,closeSize:Qt,closeIconSize:Jt,closeColorHover:Zt,closeColorPressed:ea,closeBorderRadius:ta,[Se("panePadding",l)]:Ve,[Se("tabPadding",g)]:aa,[Se("tabPaddingVertical",g)]:la,[Se("tabGap",g)]:na,[Se("tabGap",`${g}Vertical`)]:sa,[Se("tabTextColor",i)]:ra,[Se("tabTextColorActive",i)]:ia,[Se("tabTextColorHover",i)]:oa,[Se("tabTextColorDisabled",i)]:da,[Se("tabFontSize",l)]:ua},common:{cubicBezierEaseInOut:ca}}=R.value;return{"--n-bezier":ca,"--n-color-segment":Yt,"--n-bar-color":L,"--n-tab-font-size":ua,"--n-tab-text-color":ra,"--n-tab-text-color-active":ia,"--n-tab-text-color-disabled":da,"--n-tab-text-color-hover":oa,"--n-pane-text-color":Dt,"--n-tab-border-color":ke,"--n-tab-border-radius":Vt,"--n-close-size":Qt,"--n-close-icon-size":Jt,"--n-close-color-hover":Zt,"--n-close-color-pressed":ea,"--n-close-border-radius":ta,"--n-close-icon-color":N,"--n-close-icon-color-hover":te,"--n-close-icon-color-pressed":G,"--n-tab-color":ne,"--n-tab-font-weight":Nt,"--n-tab-font-weight-active":Xt,"--n-tab-padding":aa,"--n-tab-padding-vertical":la,"--n-tab-gap":na,"--n-tab-gap-vertical":sa,"--n-pane-padding-left":Xe(Ve,"left"),"--n-pane-padding-right":Xe(Ve,"right"),"--n-pane-padding-top":Xe(Ve,"top"),"--n-pane-padding-bottom":Xe(Ve,"bottom"),"--n-font-weight-strong":Gt,"--n-tab-color-segment":Kt}}),vt=z?Ra("tabs",pe(()=>`${ae.value[0]}${e.type[0]}`),bt,e):void 0;return{mergedClsPrefix:p,mergedValue:m,renderedNames:new Set,segmentCapsuleElRef:Pe,tabsPaneWrapperRef:we,tabsElRef:S,selfElRef:B,barElRef:M,addTabInstRef:Z,xScrollInstRef:$,scrollWrapperElRef:F,addTabFixed:Re,tabWrapperStyle:J,handleNavResize:De,mergedSize:ae,handleScroll:Ht,handleTabsResize:d,cssVars:z?void 0:bt,themeClass:vt?.themeClass,animationDirection:ge,renderNameListRef:v,yScrollElRef:W,handleSegmentResize:Ft,onAnimationBeforeLeave:Oe,onAnimationEnter:C,onAnimationAfterEnter:a,onRender:vt?.onRender,startReachedRef:Q,endReachedRef:h,isOverflow:y,handleButtonClick:Fe,mergedTheme:R,rtlEnabled:P,mergedPlacement:x,...qt}},render(){const{mergedClsPrefix:e,type:o,mergedPlacement:p,addTabFixed:z,addable:w,mergedSize:O,renderNameListRef:P,onRender:x,paneWrapperClass:R,paneWrapperStyle:S,startReachedRef:B,endReachedRef:M,isOverflow:F,showScrollButton:Z,handleButtonClick:$,mergedTheme:W,rtlEnabled:Q,$slots:{default:h,prefix:ee,suffix:ae}}=this;x?.();const le=h?nt(h()).filter(E=>E.type.__TAB_PANE__===!0):[],H=h?nt(h()).filter(E=>E.type.__TAB__===!0):[],m=!H.length,r=o==="card",J=o==="segment",Y=!r&&!J&&this.justifyContent;P.value=[];const be=()=>{const E=(c(),_("div",{style:We(this.tabWrapperStyle),class:K(`${e}-tabs-wrapper`)},[Y?q(()=>null):(c(),_("div",{key:1,class:K(`${e}-tabs-scroll-padding`),style:We(p==="top"||p==="bottom"?{width:`${this.tabsPadding}px`}:{height:`${this.tabsPadding}px`})},null,6)),m?(c(),_(de,{key:2},[q(()=>le.map((D,ve)=>(P.value.push(D.props.name),ot((c(),U(ut,et(D.props,{internalCreatedByPane:!0,internalLeftPadded:ve!==0&&(!Y||Y==="center"||Y==="start"||Y==="end")}),ht(D.children?{default:D.children.tab}:void 0),1040,["internalLeftPadded"]))))))],64)):(c(),_(de,{key:3},[q(()=>H.map((D,ve)=>(P.value.push(D.props.name),ot(ve!==0&&!Y?zt(D):D))))],64)),!z&&w&&r?(c(),_(de,{key:4},[q(()=>Rt(w,(m?le.length:H.length)!==0))],64)):q(()=>null),Y?q(()=>null):(c(),_("div",{key:7,class:K(`${e}-tabs-scroll-padding`),style:We({width:`${this.tabsPadding}px`})},null,6)),r?q(()=>null):(c(),_("div",{key:9,ref:"barElRef",class:K(`${e}-tabs-bar`)},null,2))],6));return c(),_("div",{ref:"tabsElRef",class:K(`${e}-tabs-nav-scroll-content`)},[r&&w?(c(),U(st,{key:0,onResize:this.handleTabsResize},{default:()=>E},1032,["onResize"])):(c(),_(de,{key:1},[q(()=>E)],64)),r?(c(),_("div",{key:2,class:K(`${e}-tabs-pad`)},null,2)):q(()=>null)],2)},ie=J?"top":p;return c(),_("div",{ref:"selfElRef",class:K([`${e}-tabs`,this.themeClass,`${e}-tabs--${o}-type`,`${e}-tabs--${O}-size`,Y&&`${e}-tabs--flex`,`${e}-tabs--${ie}`,Q&&`${e}-tabs--rtl`]),style:We(this.cssVars)},[b("div",{class:K([`${e}-tabs-nav--${o}-type`,`${e}-tabs-nav--${ie}`,`${e}-tabs-nav`])},[q(()=>yt(ee,E=>E&&(c(),_("div",{class:K(`${e}-tabs-nav__prefix`)},[q(()=>E)],2)))),J?(c(),U(st,{key:0,onResize:this.handleSegmentResize},{default:()=>(c(),_("div",{class:K(`${e}-tabs-rail`),ref:"tabsElRef"},[b("div",{class:K(`${e}-tabs-capsule`),ref:"segmentCapsuleElRef"},[b("div",{class:K(`${e}-tabs-wrapper`)},[b("div",{class:K(`${e}-tabs-tab`)},null,2)],2)],2),m?(c(),_(de,{key:0},[q(()=>le.map((E,D)=>(P.value.push(E.props.name),c(),U(ut,et(E.props,{internalCreatedByPane:!0,internalLeftPadded:D!==0}),ht(E.children?{default:E.children.tab}:void 0),1040,["internalLeftPadded"]))))],64)):(c(),_(de,{key:1},[q(()=>H.map((E,D)=>(P.value.push(E.props.name),D===0?E:zt(E))))],64))],2))},1032,["onResize"])):(c(),_(de,{key:1},[q(()=>Z&&F&&(c(),U(Pt,{mergedClsPrefix:e,type:"prev",vertical:ie==="left"||ie==="right",disabled:B,rtl:!!Q,theme:W.peers.Button,themeOverrides:W.peerOverrides.Button,onClick:$},null,8,["mergedClsPrefix","vertical","disabled","rtl","theme","themeOverrides","onClick"]))),(c(),U(st,{onResize:this.handleNavResize},{default:()=>(c(),_("div",{class:K(`${e}-tabs-nav-scroll-wrapper`),ref:"scrollWrapperElRef"},[["top","bottom"].includes(ie)?(c(),U(rl,{key:0,ref:"xScrollInstRef",onScroll:this.handleScroll},{default:be},1032,["onScroll"])):(c(),_("div",{key:1,class:K(`${e}-tabs-nav-y-scroll`),onScroll:this.handleScroll,ref:"yScrollElRef"},[q(()=>be())],42,["onScroll"]))],2))},1032,["onResize"])),q(()=>Z&&F&&(c(),U(Pt,{mergedClsPrefix:e,type:"next",vertical:ie==="left"||ie==="right",disabled:M,rtl:!!Q,theme:W.peers.Button,themeOverrides:W.peerOverrides.Button,onClick:$},null,8,["mergedClsPrefix","vertical","disabled","rtl","theme","themeOverrides","onClick"])))],64)),z&&w&&r?(c(),_(de,{key:2},[q(()=>Rt(w,!0))],64)):q(()=>null),q(()=>yt(ae,E=>E&&(c(),_("div",{class:K(`${e}-tabs-nav__suffix`)},[q(()=>E)],2))))],2),q(()=>m&&(this.animated&&(ie==="top"||ie==="bottom")?(c(),_("div",{key:1,ref:"tabsPaneWrapperRef",style:We(S),class:K([`${e}-tabs-pane-wrapper`,R])},[q(()=>_t(le,this.mergedValue,this.renderedNames,this.onAnimationBeforeLeave,this.onAnimationEnter,this.onAnimationAfterEnter,this.animationDirection))],6)):_t(le,this.mergedValue,this.renderedNames)))],6)}});function _t(e,o,p,z,w,O,P){const x=[];return e.forEach(R=>{const{name:S,displayDirective:B,"display-directive":M}=R.props,F=$=>B===$||M===$,Z=o===S;if(R.key!==void 0&&(R.key=S),Z||F("show")||F("show:lazy")&&p.has(S)){p.has(S)||p.add(S);const $=!F("if");x.push($?La(R,[[Ba,Z]]):R)}}),P?(c(),U(Aa,{name:`${P}-transition`,onBeforeLeave:z,onEnter:w,onAfterEnter:O},{default:()=>x},1032,["name","onBeforeLeave","onEnter","onAfterEnter"])):x}function Rt(e,o){return c(),U(ut,{ref:"addTabInstRef",key:"__addable",name:"__addable",internalCreatedByPane:!0,internalAddable:!0,internalLeftPadded:o,disabled:typeof e=="object"&&e.disabled},null,8,["internalLeftPadded","disabled"])}function zt(e){const o=Wa(e);return o.props?o.props.internalLeftPadded=!0:o.props={internalLeftPadded:!0},o}function ot(e){return Array.isArray(e.dynamicProps)?e.dynamicProps.includes("internalLeftPadded")||e.dynamicProps.push("internalLeftPadded"):e.dynamicProps=["internalLeftPadded"],e}const Lt=Ea("SparklesIcon",[["path",{d:"M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.581a.5.5 0 0 1 0 .964L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z",key:"4pj2yx"}],["path",{d:"M20 3v4",key:"1olli1"}],["path",{d:"M22 5h-4",key:"1gvqau"}],["path",{d:"M4 17v2",key:"vumght"}],["path",{d:"M5 18H3",key:"zchphs"}]]),pl={queued:"排队中",running:"运行中",completed:"已完成",stopped:"已停止",failed:"失败",passed:"通过",pending:"待确认",unmeasured:"未检测"},Ge=e=>pl[e]||e;function bl(e){const o=URL.createObjectURL(new Blob([e.content],{type:"text/plain;charset=utf-8"})),p=document.createElement("a");p.href=o,p.download=e.filename,document.body.append(p),p.click(),p.remove(),window.setTimeout(()=>URL.revokeObjectURL(o),1e3)}const vl={class:"smtp-presets"},ml={key:0,class:"preset-help",role:"note"},gl=ze({__name:"SMTPPresets",props:{host:{},port:{},security:{}},emits:["apply"],setup(e,{emit:o}){const p=e,z=o,w=[{value:"163",label:"163 邮箱",host:"smtp.163.com",port:465,security:"ssl",hint:"在网页版邮箱「设置 → POP3/SMTP/IMAP」开启 SMTP 服务，并使用生成的客户端授权码。"},{value:"126",label:"126 邮箱",host:"smtp.126.com",port:465,security:"ssl",hint:"在网页版邮箱「设置 → POP3/SMTP/IMAP」开启 SMTP 服务，并使用生成的客户端授权码。"},{value:"yeah",label:"Yeah 邮箱",host:"smtp.yeah.net",port:465,security:"ssl",hint:"在网页版邮箱开启 SMTP 服务，并使用客户端授权码。"},{value:"qq",label:"QQ / Foxmail 邮箱",host:"smtp.qq.com",port:465,security:"ssl",hint:"在 QQ 邮箱「设置 → 账号」开启 POP3/SMTP 或 IMAP/SMTP 服务，使用生成的授权码。Foxmail 地址也使用此服务器。"},{value:"yahoo",label:"Yahoo Mail",host:"smtp.mail.yahoo.com",port:465,security:"ssl",hint:"在 Yahoo 账号安全设置中生成应用专用密码，SMTP 使用该密码登录。用户名填写完整 Yahoo 邮箱地址。"},{value:"icloud",label:"iCloud 邮箱",host:"smtp.mail.me.com",port:587,security:"starttls",hint:"先为 Apple 账户开启双重认证，再在 account.apple.com 的「登录和安全 → App 专用密码」生成密码。发件邮箱填写完整的 iCloud 邮箱地址。"},{value:"gmail",label:"Gmail",host:"smtp.gmail.com",port:465,security:"ssl",hint:"先开启 Google 两步验证，再生成应用专用密码。若账号或组织策略未提供应用专用密码，则无法使用此密码方式登录 SMTP。"},{value:"aliyun",label:"阿里个人邮箱",host:"smtp.aliyun.com",port:465,security:"ssl",hint:"在邮箱设置中开启 SMTP 服务，并使用三方客户端授权码。此预设适用于阿里个人邮箱，企业邮箱请按管理员提供的信息自定义填写。"}],O=[{label:"自定义 SMTP（手动填写）",value:"custom"},...w.map(({label:S,value:B})=>({label:S,value:B}))],P=k("custom"),x=pe(()=>w.find(S=>S.value===P.value));Ie(()=>[p.host,p.port,p.security],([S,B,M])=>{P.value=w.find(F=>F.host===S.trim().toLowerCase()&&F.port===B&&F.security===M)?.value||"custom"},{immediate:!0});function R(S){P.value=S;const B=w.find(M=>M.value===S);B&&z("apply",{host:B.host,port:B.port,security:B.security})}return(S,B)=>(c(),_("div",vl,[s(t(j),{label:"常用邮箱配置","show-feedback":!1},{default:f(()=>[s(t(he),{value:P.value,options:O,"onUpdate:value":R},null,8,["value"])]),_:1}),B[0]||(B[0]=b("p",{class:"muted preset-description"},"选择邮箱服务可自动填入服务器、端口和加密方式；发件邮箱与授权码需自行填写。",-1)),x.value?(c(),_("div",ml,[b("strong",null,I(x.value.host)+" · "+I(x.value.port)+" · "+I(x.value.security==="ssl"?"SSL / TLS":"STARTTLS"),1),b("p",null,I(x.value.hint),1)])):oe("",!0)]))}}),Bt=ft(gl,[["__scopeId","data-v-9cf684dd"]]),yl={class:"verification-settings"},hl={class:"section-head"},xl={class:"settings-box"},wl={class:"section-head"},kl={class:"settings-box"},Sl={class:"settings-box"},Cl={class:"section-head"},Tl={class:"muted"},$l={class:"schedule-checks"},Pl={class:"muted"},_l={key:0},Rl={class:"actions"},zl={class:"actions end"},Ll=ze({__name:"VerificationSettings",props:{synchronize:{type:Function}},setup(e){const o=e,p=Et(),z=Ot(),w=k(!1),O=k(!1),P=k(!1),x=k(!1),R=k(!1),S=k(!1),B=k(""),M=k(0),F=pe(()=>S.value&&!w.value&&!B.value),Z=pe(()=>O.value||P.value||x.value||R.value||!!M.value),$=pe(()=>F.value&&!Z.value);let W=0,Q=!1;const h=Qe({configured:!1,host:"",port:465,from:"",security:"ssl",password_set:!1,password:"",clear_password:!1}),ee=k([]),ae=k([{label:"未分组",value:0}]),le=k(!1),H=k(0),m=Qe({host:"",port:465,from:"",security:"ssl",password:"",enabled:!0}),r=Qe({enabled:!1,run_at:"09:00",scope:"all",folder_id:0,sample:1,include_sold:!1,options:{timeout:10,concurrency:5,wait:60,interval:1e3},timezone:"",last_run_status:"",last_error:""}),J=k(!1),Y=[{label:"SSL / TLS",value:"ssl"},{label:"STARTTLS",value:"starttls"}],be=pe(()=>$.value&&(!r.enabled||J.value)),ie=pe(()=>r.scope==="folder"?`检测分组：${ae.value.find(C=>C.value===r.folder_id)?.label||`分组 #${r.folder_id}`}`:"全部启用主邮箱下的子邮箱"),E=[{title:"发件邮箱",key:"from",minWidth:200},{title:"服务器",key:"host",minWidth:180,render:C=>`${C.host}:${C.port}`},{title:"状态",key:"enabled",width:90,render:C=>ye(dt,{size:"small",type:C.enabled?"success":"default",bordered:!1},{default:()=>C.enabled?"启用":"停用"})},{title:"最近使用",key:"last_used_at",width:180,render:C=>qe(C.last_used_at)},{title:"操作",key:"actions",width:150,render:C=>ye("div",{class:"actions"},[ye(X,{size:"small",quaternary:!0,disabled:!$.value,onClick:()=>xe(C)},{default:()=>"编辑"}),ye(X,{size:"small",quaternary:!0,type:"error",disabled:!$.value,loading:M.value===C.id,onClick:()=>me(C)},{default:()=>"删除"})])}];async function D(){if(Q||Z.value)return;const C=++W;w.value=!0,B.value="";try{const[a,v,ge,Be]=await Promise.all([re("admin/mail/settings"),re("admin/mail/senders"),re("admin/mail/inspection-schedule"),re("admin/mail/folders")]);if(Q||C!==W)return;Object.assign(h,a,{password:"",clear_password:!1}),ee.value=v.items,Object.assign(r,ge,{include_sold:!1}),J.value=!1,ae.value=[{label:"未分组",value:0},...Be.items.map($e=>({label:$e.name,value:$e.id}))],S.value=!0}catch(a){!Q&&C===W&&(B.value=ce(a))}finally{!Q&&C===W&&(w.value=!1)}}async function ve(){if($.value){O.value=!0;try{const C=await re("admin/mail/settings","PUT",{host:h.host.trim(),port:h.port,from:h.from.trim(),security:h.security,password:h.password,clear_password:h.clear_password});Object.assign(h,C,{password:"",clear_password:!1}),p.success("默认 SMTP 配置已保存")}catch(C){p.error(ce(C))}finally{O.value=!1}}}function xe(C){$.value&&(H.value=C?.id||0,Object.assign(m,C?{host:C.host,port:C.port,from:C.from,security:C.security,password:"",enabled:C.enabled}:{host:"",port:465,from:"",security:"ssl",password:"",enabled:!0}),le.value=!0)}async function we(){if($.value){P.value=!0;try{await re(H.value?`admin/mail/senders/${H.value}`:"admin/mail/senders",H.value?"PUT":"POST",{...m,host:m.host.trim(),from:m.from.trim()}),le.value=!1,m.password="",ee.value=(await re("admin/mail/senders")).items,p.success("发件账号已保存")}catch(C){p.error(ce(C))}finally{P.value=!1}}}function me(C){$.value&&z.warning({title:"删除发件账号",content:`删除 ${C.from}？后续检测将不再使用此账号。`,positiveText:"删除",negativeText:"取消",onPositiveClick:async()=>{if(!$.value)return!1;M.value=C.id;try{await re(`admin/mail/senders/${C.id}`,"DELETE"),ee.value=ee.value.filter(a=>a.id!==C.id),p.success("发件账号已删除")}catch(a){return p.error(ce(a)),!1}finally{M.value=0}}})}async function ue(){if(be.value){x.value=!0;try{await o.synchronize();const C=await re("admin/mail/inspection-schedule","PUT",{enabled:r.enabled,run_at:r.run_at,scope:r.scope,folder_id:r.folder_id,sample:r.sample,include_sold:!1,options:r.options});Object.assign(r,C),J.value=!1,p.success("定时检测已保存")}catch(C){p.error(ce(C))}finally{x.value=!1}}}function Oe(){$.value&&z.warning({title:"立即执行收信检测",content:"使用已保存的范围和发件池向子邮箱发送测试邮件，并检查收件结果。确认立即执行？",positiveText:"确认发送并检测",negativeText:"取消",onPositiveClick:async()=>{if(!$.value)return!1;R.value=!0;try{await o.synchronize(),Object.assign(r,await re("admin/mail/inspection-schedule/run","POST")),p.success("检测任务已创建，可在检测记录中查看")}catch(C){p.error(ce(C))}finally{R.value=!1}}})}return ct(D),Mt(()=>{Q=!0,W++,S.value=!1}),(C,a)=>(c(),_("div",yl,[b("div",hl,[a[28]||(a[28]=b("div",null,[b("h2",null,"检测配置"),b("p",{class:"muted"},"配置发件账号与每日收信检测；计划执行时会自动同步邮箱与链接。")],-1)),s(t(X),{quaternary:"",loading:w.value,disabled:Z.value,onClick:D},{default:f(()=>[...a[27]||(a[27]=[A("刷新配置",-1)])]),_:1},8,["loading","disabled"])]),B.value?(c(),U(t(He),{key:0,type:"error","show-icon":!1,style:{margin:"12px 0"},role:"alert"},{default:f(()=>[A("检测配置加载失败："+I(B.value)+"。请重新加载后再操作。",1),s(t(X),{text:"",type:"error",loading:w.value,onClick:D},{default:f(()=>[...a[29]||(a[29]=[A("重新加载配置",-1)])]),_:1},8,["loading"])]),_:1})):oe("",!0),b("section",xl,[b("div",wl,[a[31]||(a[31]=b("div",null,[b("h3",null,"SMTP 发件池"),b("p",{class:"muted"},"检测邮件轮换使用启用的发件账号。授权码加密保存，编辑时留空可保留。")],-1)),s(t(X),{secondary:"",type:"primary",disabled:!$.value,onClick:a[0]||(a[0]=v=>xe())},{default:f(()=>[...a[30]||(a[30]=[A("新增发件账号",-1)])]),_:1},8,["disabled"])]),s(t(Je),{columns:E,data:ee.value,loading:w.value,bordered:!1,"row-key":v=>v.id,"scroll-x":800},null,8,["data","loading","row-key"])]),b("section",kl,[a[34]||(a[34]=b("h3",null,"默认 SMTP 配置",-1)),a[35]||(a[35]=b("p",{class:"muted"},"首次设置时可使用默认账号；添加发件池后将优先使用池中的启用账号。",-1)),s(Bt,{host:h.host,port:h.port,security:h.security,onApply:a[1]||(a[1]=v=>Object.assign(h,v))},null,8,["host","port","security"]),s(t(Ke),{"label-placement":"top",class:"settings-grid",disabled:!$.value},{default:f(()=>[s(t(j),{label:"SMTP 服务器"},{default:f(()=>[s(t(Ae),{value:h.host,"onUpdate:value":a[2]||(a[2]=v=>h.host=v),placeholder:"smtp.example.com"},null,8,["value"])]),_:1}),s(t(j),{label:"端口"},{default:f(()=>[s(t(Te),{value:h.port,"onUpdate:value":a[3]||(a[3]=v=>h.port=v),min:1,max:65535},null,8,["value"])]),_:1}),s(t(j),{label:"发件邮箱"},{default:f(()=>[s(t(Ae),{value:h.from,"onUpdate:value":a[4]||(a[4]=v=>h.from=v),placeholder:"sender@example.com"},null,8,["value"])]),_:1}),s(t(j),{label:"加密方式"},{default:f(()=>[s(t(he),{value:h.security,"onUpdate:value":a[5]||(a[5]=v=>h.security=v),options:Y},null,8,["value"])]),_:1}),s(t(j),{label:"SMTP 授权码"},{default:f(()=>[s(t(Ae),{value:h.password,"onUpdate:value":a[6]||(a[6]=v=>h.password=v),type:"password","show-password-on":"click",autocomplete:"new-password",placeholder:h.password_set?"已配置，留空保留":"请输入授权码",disabled:h.clear_password},null,8,["value","placeholder","disabled"])]),_:1}),s(t(j),{label:"移除授权码"},{default:f(()=>[s(t(wt),{checked:h.clear_password,"onUpdate:checked":a[7]||(a[7]=v=>h.clear_password=v)},{default:f(()=>[...a[32]||(a[32]=[A("清除已保存的授权码",-1)])]),_:1},8,["checked"])]),_:1})]),_:1},8,["disabled"]),s(t(X),{type:"primary",loading:O.value,disabled:!$.value,onClick:ve},{default:f(()=>[...a[33]||(a[33]=[A("保存默认 SMTP",-1)])]),_:1},8,["loading","disabled"])]),b("section",Sl,[b("div",Cl,[b("div",null,[a[36]||(a[36]=b("h3",null,"每日收信检测",-1)),b("p",Tl,"按设置的范围发送测试邮件。时区："+I(r.timezone||"系统时区")+"。",1)]),s(t(St),{value:r.enabled,"onUpdate:value":a[8]||(a[8]=v=>r.enabled=v),disabled:!$.value},{checked:f(()=>[...a[37]||(a[37]=[A("已启用",-1)])]),unchecked:f(()=>[...a[38]||(a[38]=[A("已停用",-1)])]),_:1},8,["value","disabled"])]),s(t(Ke),{"label-placement":"top",class:"settings-grid",disabled:!$.value},{default:f(()=>[s(t(j),{label:"每日执行时间"},{default:f(()=>[s(t(Ae),{value:r.run_at,"onUpdate:value":a[9]||(a[9]=v=>r.run_at=v),placeholder:"09:00"},null,8,["value"])]),_:1}),s(t(j),{label:"检测范围"},{default:f(()=>[s(t(he),{value:r.scope,"onUpdate:value":a[10]||(a[10]=v=>r.scope=v),options:[{label:"全部启用邮箱",value:"all"},{label:"指定检测分组",value:"folder"}]},null,8,["value"])]),_:1}),r.scope==="folder"?(c(),U(t(j),{key:0,label:"检测分组"},{default:f(()=>[s(t(he),{value:r.folder_id,"onUpdate:value":a[11]||(a[11]=v=>r.folder_id=v),options:ae.value},null,8,["value","options"])]),_:1})):oe("",!0),s(t(j),{label:"每组抽样"},{default:f(()=>[s(t(he),{value:r.sample,"onUpdate:value":a[12]||(a[12]=v=>r.sample=v),options:[{label:"全部检测",value:0},{label:"1 个",value:1},{label:"3 个",value:3},{label:"5 个",value:5}]},null,8,["value"])]),_:1}),s(t(j),{label:"单次请求超时（秒）"},{default:f(()=>[s(t(Te),{value:r.options.timeout,"onUpdate:value":a[13]||(a[13]=v=>r.options.timeout=v),min:1,max:60},null,8,["value"])]),_:1}),s(t(j),{label:"并发数"},{default:f(()=>[s(t(Te),{value:r.options.concurrency,"onUpdate:value":a[14]||(a[14]=v=>r.options.concurrency=v),min:1,max:10},null,8,["value"])]),_:1}),s(t(j),{label:"收信等待（秒）"},{default:f(()=>[s(t(Te),{value:r.options.wait,"onUpdate:value":a[15]||(a[15]=v=>r.options.wait=v),min:5,max:300},null,8,["value"])]),_:1}),s(t(j),{label:"发送间隔（毫秒）"},{default:f(()=>[s(t(Te),{value:r.options.interval,"onUpdate:value":a[16]||(a[16]=v=>r.options.interval=v),min:200,max:1e4,step:100},null,8,["value"])]),_:1})]),_:1},8,["disabled"]),b("div",$l,[r.enabled?(c(),U(t(wt),{key:0,checked:J.value,"onUpdate:checked":a[17]||(a[17]=v=>J.value=v),disabled:!$.value},{default:f(()=>[...a[39]||(a[39]=[A("确认按此计划自动发送测试邮件",-1)])]),_:1},8,["checked","disabled"])):oe("",!0)]),r.last_error?(c(),U(t(He),{key:0,type:"error","show-icon":!1,style:{margin:"12px 0"}},{default:f(()=>[A(I(r.last_error),1)]),_:1})):oe("",!0),b("p",Pl,[A("检测范围："+I(ie.value),1),a[40]||(a[40]=b("br",null,null,-1)),A("上次执行："+I(t(qe)(r.last_run_at))+" · "+I(r.last_run_status||"尚未运行"),1),r.last_run_task_id?(c(),_("span",_l," · 任务 #"+I(r.last_run_task_id),1)):oe("",!0),a[41]||(a[41]=b("br",null,null,-1)),A("下次执行："+I(t(qe)(r.next_run_at)),1)]),b("div",Rl,[s(t(X),{type:"primary",loading:x.value,disabled:!be.value,onClick:ue},{default:f(()=>[...a[42]||(a[42]=[A("保存定时检测",-1)])]),_:1},8,["loading","disabled"]),s(t(X),{secondary:"",loading:R.value,disabled:!$.value,onClick:Oe},{default:f(()=>[...a[43]||(a[43]=[A("立即执行已保存的计划",-1)])]),_:1},8,["loading","disabled"])])]),s(t(Ut),{show:le.value,"onUpdate:show":a[26]||(a[26]=v=>le.value=v),preset:"card",title:H.value?"编辑发件账号":"新增发件账号",style:{width:"min(600px,calc(100vw - 32px))"}},{footer:f(()=>[b("div",zl,[s(t(X),{onClick:a[25]||(a[25]=v=>le.value=!1)},{default:f(()=>[...a[46]||(a[46]=[A("取消",-1)])]),_:1}),s(t(X),{type:"primary",loading:P.value,disabled:!$.value,onClick:we},{default:f(()=>[...a[47]||(a[47]=[A("保存账号",-1)])]),_:1},8,["loading","disabled"])])]),default:f(()=>[s(Bt,{host:m.host,port:m.port,security:m.security,onApply:a[18]||(a[18]=v=>Object.assign(m,v))},null,8,["host","port","security"]),s(t(Ke),{"label-placement":"top",class:"settings-grid",disabled:!$.value},{default:f(()=>[s(t(j),{label:"SMTP 服务器"},{default:f(()=>[s(t(Ae),{value:m.host,"onUpdate:value":a[19]||(a[19]=v=>m.host=v),placeholder:"smtp.example.com"},null,8,["value"])]),_:1}),s(t(j),{label:"端口"},{default:f(()=>[s(t(Te),{value:m.port,"onUpdate:value":a[20]||(a[20]=v=>m.port=v),min:1,max:65535},null,8,["value"])]),_:1}),s(t(j),{label:"发件邮箱"},{default:f(()=>[s(t(Ae),{value:m.from,"onUpdate:value":a[21]||(a[21]=v=>m.from=v),placeholder:"sender@example.com"},null,8,["value"])]),_:1}),s(t(j),{label:"加密方式"},{default:f(()=>[s(t(he),{value:m.security,"onUpdate:value":a[22]||(a[22]=v=>m.security=v),options:Y},null,8,["value"])]),_:1}),s(t(j),{label:"SMTP 授权码"},{default:f(()=>[s(t(Ae),{value:m.password,"onUpdate:value":a[23]||(a[23]=v=>m.password=v),type:"password","show-password-on":"click",autocomplete:"new-password",placeholder:H.value?"留空保留现有授权码":"请输入授权码"},null,8,["value","placeholder"])]),_:1}),s(t(j),{label:"状态"},{default:f(()=>[s(t(St),{value:m.enabled,"onUpdate:value":a[24]||(a[24]=v=>m.enabled=v)},{checked:f(()=>[...a[44]||(a[44]=[A("启用",-1)])]),unchecked:f(()=>[...a[45]||(a[45]=[A("停用",-1)])]),_:1},8,["value"])]),_:1})]),_:1},8,["disabled"])]),_:1},8,["show","title"])]))}}),Bl=ft(Ll,[["__scopeId","data-v-cd95d751"]]),Al={class:"verification-page"},Wl={class:"page-title"},Il={class:"surface verification-scope"},El={key:0,class:"scope-counts"},Ol={class:"surface verification-panel"},Ml={class:"verification-tab"},Ul={class:"section-head"},jl={class:"row inspection-actions"},Hl={class:"verification-tab"},ql={class:"section-head"},Fl={class:"muted"},Dl={class:"row"},Nl={key:0,class:"retry-summary"},Vl={key:0,class:"retry-skipped"},Xl={key:1,class:"muted"},Yl=ze({__name:"Verification",setup(e){const o=Et(),p=Ot(),z=Oa(),w=Ma(),O=pe(()=>z.query.tab==="settings"?"settings":"inspection"),P=k(!1),x=k(""),R=k(!1),S=k(null),B=k([]),M=k([]),F=k(1),Z=k(0),$=k("link"),W=k("all"),Q=k(1),h=k([]),ee=k(!1),ae=k(null),le=pe(()=>(S.value?.accounts||[]).filter(d=>d.mapped&&!d.conflict).map(d=>({label:d.email,value:d.mailbox_id}))),H=Qe({timeout:10,concurrency:5,wait:60,interval:1e3}),m=k(!1),r=k(null),J=k(!1),Y=k(1),be=k("");let ie=0,E=0,D=0,ve=0,xe,we=!1,me,ue=!1;const Oe=[{title:"任务",key:"id",width:70,render:d=>`#${d.id}`},{title:"类型",key:"mode",width:100,render:d=>d.mode==="link"?"链接检测":"收信验证"},{title:"进度",key:"progress",width:110,render:d=>`${d.processed}/${d.total}`},{title:"结果",key:"passed",minWidth:160,render:d=>`${d.passed} 通过 · ${d.failed} 失败 · ${d.pending} 待确认`},{title:"状态",key:"status",width:100,render:d=>ye(dt,{size:"small",bordered:!1},{default:()=>Ge(d.status)})},{title:"创建时间",key:"created_at",width:175,render:d=>qe(d.created_at)},{title:"操作",key:"actions",width:245,render:d=>ye("div",{class:"table-actions"},[ye(X,{size:"small",quaternary:!0,onClick:()=>Le(d.id)},{default:()=>"详情"}),ye(X,{size:"small",quaternary:!0,onClick:()=>Re(d.id)},{default:()=>"报告"}),["queued","running"].includes(d.status)?ye(X,{size:"small",quaternary:!0,type:"warning",loading:ae.value===-d.id,onClick:()=>De(d.id)},{default:()=>"停止"}):ye(X,{size:"small",quaternary:!0,loading:ae.value===-d.id,onClick:()=>Ue(d.id)},{default:()=>"复检未通过项"})])}],C=[{title:"子邮箱",key:"email",minWidth:200},{title:"主邮箱",key:"parent_email",minWidth:200,ellipsis:{tooltip:!0},render:d=>d.parent_email||"—"},{title:"结果",key:"result",width:100,render:d=>Ge(d.result)},{title:"说明",key:"message",minWidth:260},{title:"HTTP",key:"status_code",width:80,render:d=>d.status_code||"—"},{title:"发件邮箱",key:"sender_email",minWidth:200,render:d=>d.sender_email||"—"},{title:"检测时间",key:"checked_at",width:175,render:d=>qe(d.checked_at)}],a=[{title:"子邮箱",key:"email",minWidth:200},{title:"主邮箱",key:"parent_email",minWidth:200,render:d=>d.parent_email||"—"},{title:"跳过原因",key:"reason",minWidth:240}];async function v(){const d=++D,n=await re("admin/mail/mapping/preview?page=1&limit=1");if(d===D){S.value=n;const y=new Set(le.value.map(fe=>fe.value));h.value=h.value.filter(fe=>y.has(fe))}}async function ge(){const d=++ie,n=await re("admin/mail/inspections?"+xt({page:F.value,limit:25}));d===ie&&(M.value=n.items,Z.value=n.total)}async function Be(){const d=++ve;P.value=!0,x.value="";const n=await Promise.allSettled([v(),ge()]);d===ve&&(x.value=n.flatMap((y,fe)=>y.status==="rejected"?[`${["检测范围","检测任务"][fe]}：${ce(y.reason)}`]:[]).join("；"),P.value=!1)}async function $e(d=!1){if(me)return me;R.value=!0,me=(async()=>{const n=await re("admin/mail/mapping","POST",{include_links:!0});B.value=n.conflicts,await v(),d&&o.success(`检测范围已同步，新增 ${n.created_accounts} 个主邮箱、${n.created_links} 个子邮箱`)})();try{await me}finally{R.value=!1,me=void 0}}async function tt(){try{await $e(!0)}catch(d){o.error(ce(d))}}async function Fe(){try{await ge()}catch(d){o.error(ce(d))}}function Ee(d){w.replace({path:"/verification",query:d==="settings"?{tab:"settings"}:{}})}async function Me(){if(!(ee.value||R.value)){if(W.value==="master"&&!h.value.length)return o.warning("请选择需要验证的主邮箱");$.value==="inbox"?p.warning({title:"确认发送测试邮件",content:"收信验证将使用已配置的发件邮箱，向所选范围中的子邮箱发送测试邮件，再通过取信链接查找本次邮件标识。",positiveText:"确认并发送",negativeText:"取消",onPositiveClick:()=>Pe(!0)}):await Pe(!1)}}async function Pe(d){ee.value=!0;try{if(await $e(),W.value==="master"&&!h.value.length)throw new Error("所选主邮箱已停用或删除，请重新选择");const n=await re("admin/mail/inspections","POST",{mode:$.value,scope:W.value,master_ids:W.value==="master"?h.value:[],sample:Q.value,include_sold:!1,confirm_send:d,options:H});return o.success("检测任务已排队"),F.value=1,await ge(),await Le(n.id),!0}catch(n){return o.error(ce(n)),!1}finally{ee.value=!1}}async function Le(d){m.value=!0,r.value=null,Y.value=1,be.value="",await _e(d)}async function _e(d=r.value?.task.id,n=!1){if(!d)return;const y=++E;n||(J.value=!0);try{const fe=await re(`admin/mail/inspections/${d}?`+xt({page:Y.value,limit:25,result:be.value}));y===E&&(r.value=fe)}catch(fe){n||o.error(ce(fe))}finally{y===E&&(J.value=!1)}}function at(){Y.value=1,_e()}function De(d){p.warning({title:"停止检测任务",content:"已完成的结果会保留，尚未完成的项目可稍后复检。",positiveText:"停止任务",negativeText:"继续检测",onPositiveClick:async()=>{ae.value=-d;try{return await re(`admin/mail/inspections/${d}/stop`,"POST",{}),o.success("任务已停止"),await ge(),r.value?.task.id===d&&await _e(d),!0}catch(n){return o.error(ce(n)),!1}finally{ae.value=null}}})}async function Ue(d){ae.value=-d;try{const n=await re(`admin/mail/inspections/${d}/retry`,"POST",{}),y=n.retry_info.skipped.length;o.success(`已创建 ${n.retry_info.retried} 项复检${y?`，跳过 ${y} 项，原因见任务详情`:""}；收信验证不重复发信`),await ge(),await Le(n.id)}catch(n){o.error(ce(n))}finally{ae.value=null}}async function Re(d){try{bl(await re(`admin/mail/inspections/${d}/export`))}catch(n){o.error(ce(n))}}async function lt(){if(!(we||document.hidden||!M.value.some(d=>["queued","running"].includes(d.status))&&!m.value)){we=!0;try{await ge(),m.value&&r.value&&await _e(r.value.task.id,!0)}catch{}finally{we=!1}}}return Ie(m,d=>{d||(E++,r.value=null,J.value=!1)}),ct(async()=>{try{await $e()}catch(d){ue||o.error(ce(d))}ue||(await Be(),ue||(xe=setInterval(lt,5e3)))}),Mt(()=>{ue=!0,ie++,E++,D++,ve++,xe&&clearInterval(xe)}),(d,n)=>(c(),_("div",Al,[b("div",Wl,[n[19]||(n[19]=b("div",null,[b("h1",null,"邮箱验证"),b("p",null,"检查子邮箱取信链接，并通过测试邮件验证实际收信。")],-1)),s(t(X),{quaternary:"",loading:P.value,onClick:Be},{default:f(()=>[s(t(Ua),{size:16,class:"icon-before"}),n[18]||(n[18]=A("刷新数据",-1))]),_:1},8,["loading"])]),x.value?(c(),U(t(He),{key:0,type:"error","show-icon":!1,class:"verification-alert"},{default:f(()=>[A(I(x.value)+" ",1),s(t(X),{text:"",type:"error",loading:P.value,onClick:Be},{default:f(()=>[...n[20]||(n[20]=[A("重新加载",-1)])]),_:1},8,["loading"])]),_:1})):oe("",!0),b("section",Il,[b("div",null,[n[24]||(n[24]=b("h2",null,"检测范围",-1)),n[25]||(n[25]=b("p",{class:"muted"},"使用邮箱管理中的启用主邮箱及其子邮箱链接；新增后可同步，提交检测时也会自动同步。",-1)),S.value?(c(),_("div",El,[b("span",null,[n[21]||(n[21]=A("主邮箱 ",-1)),b("strong",null,I(S.value.summary.active_accounts),1)]),b("span",null,[n[22]||(n[22]=A("子邮箱链接 ",-1)),b("strong",null,I(S.value.summary.links),1)]),b("span",null,[n[23]||(n[23]=A("已同步 ",-1)),b("strong",null,I(S.value.summary.mapped_links),1)]),S.value.summary.conflicts?(c(),U(t(dt),{key:0,type:"warning",bordered:!1},{default:f(()=>[A(I(S.value.summary.conflicts)+" 项冲突",1)]),_:1})):oe("",!0)])):oe("",!0)]),s(t(X),{secondary:"",loading:R.value,disabled:ee.value,onClick:tt},{default:f(()=>[...n[26]||(n[26]=[A("同步邮箱与链接",-1)])]),_:1},8,["loading","disabled"])]),B.value.length?(c(),U(t(He),{key:1,type:"warning",class:"verification-alert","show-icon":!1},{default:f(()=>[b("details",null,[b("summary",null,I(B.value.length)+" 个邮箱重复或归属冲突，已跳过；展开查看",1),(c(!0),_(de,null,ja(B.value,(y,fe)=>(c(),_("p",{key:fe},I(y.email)+"："+I(y.reason.replaceAll("销售库存","检测范围").replaceAll("库存","检测范围")),1))),128))])]),_:1})):oe("",!0),b("section",Ol,[s(t(fl),{type:"line",animated:!1,value:O.value,"onUpdate:value":Ee},{default:f(()=>[s(t($t),{name:"inspection","display-directive":"show:lazy"},{tab:f(()=>[b("span",Ml,[s(t(Lt),{size:16}),n[27]||(n[27]=A("检测任务",-1))])]),default:f(()=>[b("div",Ul,[n[29]||(n[29]=b("div",null,[b("h2",null,"验证邮箱"),b("p",{class:"muted"},"链接检测会确认取信是否正常；收信验证会发送测试邮件，需先配置发件邮箱。")],-1)),s(t(X),{text:"",type:"primary",onClick:n[0]||(n[0]=y=>Ee("settings"))},{default:f(()=>[...n[28]||(n[28]=[A("发件邮箱与定时检测",-1)])]),_:1})]),s(t(Ke),{"label-placement":"top",class:"form-grid",disabled:ee.value||R.value},{default:f(()=>[s(t(j),{label:"检测类型"},{default:f(()=>[s(t(he),{value:$.value,"onUpdate:value":n[1]||(n[1]=y=>$.value=y),options:[{label:"链接检测",value:"link"},{label:"收信验证（发送邮件）",value:"inbox"}]},null,8,["value"])]),_:1}),s(t(j),{label:"检测范围"},{default:f(()=>[s(t(he),{value:W.value,"onUpdate:value":n[2]||(n[2]=y=>W.value=y),options:[{label:"全部启用邮箱",value:"all"},{label:"指定主邮箱",value:"master"}]},null,8,["value"])]),_:1}),W.value==="master"?(c(),U(t(j),{key:0,label:"主邮箱",class:"master-select"},{default:f(()=>[s(t(he),{value:h.value,"onUpdate:value":n[3]||(n[3]=y=>h.value=y),multiple:"",filterable:"",clearable:"",options:le.value,placeholder:"搜索并选择主邮箱"},null,8,["value","options"])]),_:1})):oe("",!0),s(t(j),{label:"每个主邮箱抽样"},{default:f(()=>[s(t(he),{value:Q.value,"onUpdate:value":n[4]||(n[4]=y=>Q.value=y),options:[{label:"全部子邮箱",value:0},{label:"每组 1 个",value:1},{label:"每组 3 个",value:3},{label:"每组 5 个",value:5}]},null,8,["value"])]),_:1}),s(t(j),{label:"请求超时（秒）"},{default:f(()=>[s(t(Te),{value:H.timeout,"onUpdate:value":n[5]||(n[5]=y=>H.timeout=y),min:1,max:60,precision:0},null,8,["value"])]),_:1}),s(t(j),{label:"并发数"},{default:f(()=>[s(t(Te),{value:H.concurrency,"onUpdate:value":n[6]||(n[6]=y=>H.concurrency=y),min:1,max:10,precision:0},null,8,["value"])]),_:1}),$.value==="inbox"?(c(),U(t(j),{key:1,label:"收信等待（秒）"},{default:f(()=>[s(t(Te),{value:H.wait,"onUpdate:value":n[7]||(n[7]=y=>H.wait=y),min:5,max:300,precision:0},null,8,["value"])]),_:1})):oe("",!0),$.value==="inbox"?(c(),U(t(j),{key:2,label:"发信间隔（毫秒）"},{default:f(()=>[s(t(Te),{value:H.interval,"onUpdate:value":n[8]||(n[8]=y=>H.interval=y),min:200,max:1e4,precision:0},null,8,["value"])]),_:1})):oe("",!0)]),_:1},8,["disabled"]),b("div",jl,[s(t(X),{type:"primary",loading:ee.value,disabled:R.value,onClick:Me},{default:f(()=>[s(t(Lt),{size:15,class:"icon-before"}),A(I($.value==="inbox"?"确认发信并提交":"提交链接检测"),1)]),_:1},8,["loading","disabled"]),s(t(X),{quaternary:"",onClick:Fe},{default:f(()=>[...n[30]||(n[30]=[A("刷新任务",-1)])]),_:1}),n[31]||(n[31]=b("span",{class:"muted"},"停用、过期或已删除来源的链接会自动排除。",-1))]),s(t(Je),{columns:Oe,data:M.value,loading:P.value,bordered:!1,"row-key":y=>y.id,"scroll-x":1070},null,8,["data","loading","row-key"]),s(t(kt),{page:F.value,"onUpdate:page":[n[9]||(n[9]=y=>F.value=y),Fe],"item-count":Z.value,"page-size":25,simple:""},null,8,["page","item-count"])]),_:1}),s(t($t),{name:"settings","display-directive":"show:lazy"},{tab:f(()=>[b("span",Hl,[s(t(Da),{size:16}),n[32]||(n[32]=A("检测配置",-1))])]),default:f(()=>[s(Bl,{synchronize:$e})]),_:1})]),_:1},8,["value"])]),s(t(Ut),{show:m.value,"onUpdate:show":n[17]||(n[17]=y=>m.value=y),preset:"card",title:"检测任务详情",class:"verification-modal",style:{width:"min(1120px,calc(100vw - 32px))"}},{default:f(()=>[s(t(Ha),{show:J.value},{default:f(()=>[r.value?(c(),_(de,{key:0},[b("div",ql,[b("div",null,[b("h2",null,"任务 #"+I(r.value.task.id)+" · "+I(t(Ge)(r.value.task.status)),1),b("p",Fl,"已处理 "+I(r.value.task.processed)+"/"+I(r.value.task.total)+" · 通过 "+I(r.value.task.passed)+" · 失败 "+I(r.value.task.failed)+" · 待确认 "+I(r.value.task.pending),1)]),b("div",Dl,[s(t(X),{onClick:n[10]||(n[10]=y=>_e())},{default:f(()=>[...n[33]||(n[33]=[A("刷新",-1)])]),_:1}),s(t(X),{onClick:n[11]||(n[11]=y=>Re(r.value.task.id))},{default:f(()=>[...n[34]||(n[34]=[A("下载报告",-1)])]),_:1}),["queued","running"].includes(r.value.task.status)?(c(),U(t(X),{key:0,type:"warning",loading:ae.value===-r.value.task.id,onClick:n[12]||(n[12]=y=>De(r.value.task.id))},{default:f(()=>[...n[35]||(n[35]=[A("停止",-1)])]),_:1},8,["loading"])):(c(),U(t(X),{key:1,loading:ae.value===-r.value.task.id,onClick:n[13]||(n[13]=y=>Ue(r.value.task.id))},{default:f(()=>[...n[36]||(n[36]=[A("复检未通过项",-1)])]),_:1},8,["loading"]))])]),r.value.retry_info?(c(),_("div",Nl,[s(t(He),{type:r.value.retry_info.skipped.length?"warning":"info","show-icon":!1},{default:f(()=>[A("来源任务 #"+I(r.value.retry_info.source_id)+"：原未通过 "+I(r.value.retry_info.requested)+" 项，本次复检 "+I(r.value.retry_info.retried)+" 项，跳过 "+I(r.value.retry_info.skipped.length)+" 项。",1)]),_:1},8,["type"]),r.value.retry_info.skipped.length?(c(),_("details",Vl,[b("summary",null,"查看跳过项目及原因（"+I(r.value.retry_info.skipped.length)+"）",1),s(t(Je),{columns:a,data:r.value.retry_info.skipped,"row-key":y=>y.email,"scroll-x":640,pagination:{pageSize:5}},null,8,["data","row-key"])])):oe("",!0)])):oe("",!0),r.value.task.mode==="inbox"?(c(),_("p",Xl,"复检沿用原测试邮件标识，不会重新发送测试邮件。")):oe("",!0),s(t(he),{value:be.value,"onUpdate:value":[n[14]||(n[14]=y=>be.value=y),at],class:"result-filter",options:[{label:"全部结果",value:""},...["passed","failed","pending"].map(y=>({label:t(Ge)(y),value:y}))]},null,8,["value","options"]),s(t(Je),{columns:C,data:r.value.results,"row-key":y=>y.id,"scroll-x":1315},null,8,["data","row-key"]),s(t(kt),{page:Y.value,"onUpdate:page":[n[15]||(n[15]=y=>Y.value=y),n[16]||(n[16]=y=>_e())],"item-count":r.value.total,"page-size":25,simple:""},null,8,["page","item-count"])],64)):J.value?oe("",!0):(c(),U(t(qa),{key:1,description:"检测详情加载失败"}))]),_:1},8,["show"])]),_:1},8,["show"])]))}}),an=ft(Yl,[["__scopeId","data-v-dbe1f6cf"]]);export{an as default};
