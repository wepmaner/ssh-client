export namespace app {
	
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

