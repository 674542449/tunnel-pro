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

