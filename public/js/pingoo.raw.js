(function(w, d) {
    const cfg = {apiUrl: '', siteId: ''};
    function getScriptConfig() {
        for (const s of d.getElementsByTagName('script')) {
            const siteId = s.getAttribute('site-id');
            if (siteId) {
                try {
                    const url = new URL(s.src);
                    cfg.apiUrl = url.origin + '/send';
                } catch {
                    cfg.apiUrl = '/send';
                }
                cfg.siteId = siteId;
                cfg.userId = s.getAttribute('user-id') || '';
                return;
            }
        }
    }
    function getSessionId(){
        let k="pingoo_sess",t=18e5,n=Date.now(),d=JSON.parse(localStorage.getItem(k)||"{}");
        if(!d.id||n-d.t>t)d={id:"s_"+Math.random().toString(36).slice(2)+"_"+n,t:n};
        else d.t=n;
        localStorage.setItem(k,JSON.stringify(d));
        return d.id;
    }
    function sendEvent(type, value) {
        if (!cfg.siteId) return;
        fetch(cfg.apiUrl, {
            method: 'POST',
            body: JSON.stringify({
                session_id: getSessionId(),
                site_id: cfg.siteId,
                user_id: cfg.userId || '',
                url: w.location.pathname,
                referrer: d.referrer,
                event_type: type,
                event_value: value || '',
                screen: screen.width + 'x' + screen.height
            })
        });
    }
    function init() {
        getScriptConfig();
        if (!cfg.siteId) {
            console.error('请配置site-id');
            return;
        }
        sendEvent('page_view', '');
        d.addEventListener('click', e => {
            const el = e.target.closest('[pingoo-event]');
            if (el) sendEvent(el.getAttribute('pingoo-event'), el.getAttribute('pingoo-event-value') || '');
        });
    }
    d.readyState === 'loading' ? d.addEventListener('DOMContentLoaded', init) : init();
})(window, document);