export namespace domain {
	
	export class CanvasNode {
	    id: number;
	    kind: string;
	    conversation_id?: number;
	    x: number;
	    y: number;
	    width: number;
	    height: number;
	    z: number;
	    color?: string;
	    body?: string;
	
	    static createFrom(source: any = {}) {
	        return new CanvasNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.conversation_id = source["conversation_id"];
	        this.x = source["x"];
	        this.y = source["y"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.z = source["z"];
	        this.color = source["color"];
	        this.body = source["body"];
	    }
	}
	export class Conversation {
	    id: number;
	    title: string;
	    kind: string;
	    providers: string[];
	    // Go type: time
	    created_at: any;
	
	    static createFrom(source: any = {}) {
	        return new Conversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.kind = source["kind"];
	        this.providers = source["providers"];
	        this.created_at = this.convertValues(source["created_at"], null);
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
	export class Canvas {
	    conversations: Conversation[];
	    nodes: CanvasNode[];
	
	    static createFrom(source: any = {}) {
	        return new Canvas(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conversations = this.convertValues(source["conversations"], Conversation);
	        this.nodes = this.convertValues(source["nodes"], CanvasNode);
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
	
	export class CanvasNodePatch {
	    id: number;
	    x?: number;
	    y?: number;
	    width?: number;
	    height?: number;
	    z?: number;
	    color?: string;
	    body?: string;
	
	    static createFrom(source: any = {}) {
	        return new CanvasNodePatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.x = source["x"];
	        this.y = source["y"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.z = source["z"];
	        this.color = source["color"];
	        this.body = source["body"];
	    }
	}
	export class ChatResponse {
	    id: number;
	    turn_id: number;
	    provider: string;
	    status: string;
	    content?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.turn_id = source["turn_id"];
	        this.provider = source["provider"];
	        this.status = source["status"];
	        this.content = source["content"];
	        this.error = source["error"];
	    }
	}
	export class ChatTurn {
	    id: number;
	    prompt: string;
	    // Go type: time
	    created_at: any;
	    responses: ChatResponse[];
	
	    static createFrom(source: any = {}) {
	        return new ChatTurn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.prompt = source["prompt"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.responses = this.convertValues(source["responses"], ChatResponse);
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
	
	export class NewConversation {
	    title: string;
	    kind: string;
	    providers: string[];
	    x: number;
	    y: number;
	
	    static createFrom(source: any = {}) {
	        return new NewConversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.kind = source["kind"];
	        this.providers = source["providers"];
	        this.x = source["x"];
	        this.y = source["y"];
	    }
	}
	export class NewNote {
	    body: string;
	    color: string;
	    x: number;
	    y: number;
	
	    static createFrom(source: any = {}) {
	        return new NewNote(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.body = source["body"];
	        this.color = source["color"];
	        this.x = source["x"];
	        this.y = source["y"];
	    }
	}
	export class Provider {
	    name: string;
	    kind: string;
	    available: boolean;
	    command?: string;
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.available = source["available"];
	        this.command = source["command"];
	    }
	}
	export class Stage {
	    id: number;
	    run_id: number;
	    position: number;
	    name: string;
	    command: string[];
	    status: string;
	    exit_code?: number;
	    output?: string;
	
	    static createFrom(source: any = {}) {
	        return new Stage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.run_id = source["run_id"];
	        this.position = source["position"];
	        this.name = source["name"];
	        this.command = source["command"];
	        this.status = source["status"];
	        this.exit_code = source["exit_code"];
	        this.output = source["output"];
	    }
	}
	export class Run {
	    id: number;
	    project: string;
	    status: string;
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    updated_at: any;
	    stages?: Stage[];
	
	    static createFrom(source: any = {}) {
	        return new Run(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.project = source["project"];
	        this.status = source["status"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.updated_at = this.convertValues(source["updated_at"], null);
	        this.stages = this.convertValues(source["stages"], Stage);
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
	export class Snapshot {
	    healthy: boolean;
	    version: string;
	    providers: Provider[];
	    runs: Run[];
	    turns: ChatTurn[];
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.healthy = source["healthy"];
	        this.version = source["version"];
	        this.providers = this.convertValues(source["providers"], Provider);
	        this.runs = this.convertValues(source["runs"], Run);
	        this.turns = this.convertValues(source["turns"], ChatTurn);
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

