export namespace app {
	
	export class GroupDTO {
	    id: string;
	    name: string;
	    host_count: number;
	    connected_count: number;
	
	    static createFrom(source: any = {}) {
	        return new GroupDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.host_count = source["host_count"];
	        this.connected_count = source["connected_count"];
	    }
	}
	export class HostDTO {
	    id: string;
	    name: string;
	    address: string;
	    port: number;
	    user: string;
	    group_id: string;
	    group_name: string;
	    key_id: string;
	    key_name: string;
	    os: string;
	    connected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HostDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.address = source["address"];
	        this.port = source["port"];
	        this.user = source["user"];
	        this.group_id = source["group_id"];
	        this.group_name = source["group_name"];
	        this.key_id = source["key_id"];
	        this.key_name = source["key_name"];
	        this.os = source["os"];
	        this.connected = source["connected"];
	    }
	}
	export class KeyDTO {
	    id: string;
	    name: string;
	    type: string;
	    format: string;
	    encrypted: boolean;
	    public_line: string;
	    comment: string;
	    fingerprint: string;
	    source: string;
	    created_at: string;
	    host_ids: string[];
	
	    static createFrom(source: any = {}) {
	        return new KeyDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.format = source["format"];
	        this.encrypted = source["encrypted"];
	        this.public_line = source["public_line"];
	        this.comment = source["comment"];
	        this.fingerprint = source["fingerprint"];
	        this.source = source["source"];
	        this.created_at = source["created_at"];
	        this.host_ids = source["host_ids"];
	    }
	}
	export class SessionDTO {
	    id: string;
	    host_id: string;
	    host_name: string;
	    state: string;
	    latency_ms: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.host_id = source["host_id"];
	        this.host_name = source["host_name"];
	        this.state = source["state"];
	        this.latency_ms = source["latency_ms"];
	    }
	}
	export class VaultCountsDTO {
	    keys: number;
	    hosts: number;
	
	    static createFrom(source: any = {}) {
	        return new VaultCountsDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.keys = source["keys"];
	        this.hosts = source["hosts"];
	    }
	}
	export class VaultStateDTO {
	    locked: boolean;
	    has_password: boolean;
	    corrupt: boolean;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new VaultStateDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.locked = source["locked"];
	        this.has_password = source["has_password"];
	        this.corrupt = source["corrupt"];
	        this.error = source["error"];
	    }
	}

}

export namespace model {
	
	export class WindowState {
	    x: number;
	    y: number;
	    w: number;
	    h: number;
	    maximized: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WindowState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.x = source["x"];
	        this.y = source["y"];
	        this.w = source["w"];
	        this.h = source["h"];
	        this.maximized = source["maximized"];
	    }
	}
	export class Settings {
	    terminal_font: string;
	    terminal_size: number;
	    copy_on_select: boolean;
	    blink_cursor: boolean;
	    webgl: boolean;
	    lock_after_min: number;
	    window: WindowState;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.terminal_font = source["terminal_font"];
	        this.terminal_size = source["terminal_size"];
	        this.copy_on_select = source["copy_on_select"];
	        this.blink_cursor = source["blink_cursor"];
	        this.webgl = source["webgl"];
	        this.lock_after_min = source["lock_after_min"];
	        this.window = this.convertValues(source["window"], WindowState);
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

}

