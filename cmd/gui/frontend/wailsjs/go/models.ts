export namespace main {
	
	export class NodeInfo {
	    id: number;
	    name: string;
	    region: string;
	    flag: string;
	    latency: number;
	
	    static createFrom(source: any = {}) {
	        return new NodeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.region = source["region"];
	        this.flag = source["flag"];
	        this.latency = source["latency"];
	    }
	}
	export class OrderInfo {
	    id: number;
	    plan: string;
	    days: number;
	    amount: number;
	    status: string;
	    created_at: number;
	    paid_at: number;
	
	    static createFrom(source: any = {}) {
	        return new OrderInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.plan = source["plan"];
	        this.days = source["days"];
	        this.amount = source["amount"];
	        this.status = source["status"];
	        this.created_at = source["created_at"];
	        this.paid_at = source["paid_at"];
	    }
	}
	export class PlanInfo {
	    id: number;
	    name: string;
	    days: number;
	    price: number;
	
	    static createFrom(source: any = {}) {
	        return new PlanInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.days = source["days"];
	        this.price = source["price"];
	    }
	}
	export class ProfileInfo {
	    id: number;
	    email: string;
	    role: string;
	    active: boolean;
	    expires_at: number;
	    created_at: number;
	    trial_used: boolean;
	    upload: number;
	    download: number;
	    plans: PlanInfo[];
	
	    static createFrom(source: any = {}) {
	        return new ProfileInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.email = source["email"];
	        this.role = source["role"];
	        this.active = source["active"];
	        this.expires_at = source["expires_at"];
	        this.created_at = source["created_at"];
	        this.trial_used = source["trial_used"];
	        this.upload = source["upload"];
	        this.download = source["download"];
	        this.plans = this.convertValues(source["plans"], PlanInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SpeedInfo {
	    upload: number;
	    download: number;
	
	    static createFrom(source: any = {}) {
	        return new SpeedInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.upload = source["upload"];
	        this.download = source["download"];
	    }
	}
	export class StatusInfo {
	    connected: boolean;
	    nodeName: string;
	    nodeId: number;
	
	    static createFrom(source: any = {}) {
	        return new StatusInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.nodeName = source["nodeName"];
	        this.nodeId = source["nodeId"];
	    }
	}
	export class UpdateInfo {
	    available: boolean;
	    version: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.version = source["version"];
	        this.url = source["url"];
	    }
	}

}

