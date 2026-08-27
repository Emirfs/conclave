export namespace domain {
	
	export class CanvasLink {
	    id: number;
	    source_id: number;
	    target_id: number;
	    mode: string;
	    max_rounds: number;
	    until_done: boolean;
	    briefing?: string;
	
	    static createFrom(source: any = {}) {
	        return new CanvasLink(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.source_id = source["source_id"];
	        this.target_id = source["target_id"];
	        this.mode = source["mode"];
	        this.max_rounds = source["max_rounds"];
	        this.until_done = source["until_done"];
	        this.briefing = source["briefing"];
	    }
	}
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
	export class CardRun {
	    id: number;
	    status: string;
	    step_name?: string;
	    exit_code: number;
	    output?: string;
	    started_at: string;
	    finished_at?: string;
	
	    static createFrom(source: any = {}) {
	        return new CardRun(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.status = source["status"];
	        this.step_name = source["step_name"];
	        this.exit_code = source["exit_code"];
	        this.output = source["output"];
	        this.started_at = source["started_at"];
	        this.finished_at = source["finished_at"];
	    }
	}
	export class CardStep {
	    name: string;
	    command: string;
	    timeout_seconds?: number;
	
	    static createFrom(source: any = {}) {
	        return new CardStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.command = source["command"];
	        this.timeout_seconds = source["timeout_seconds"];
	    }
	}
	export class LoopConfig {
	    mode: string;
	    interval_seconds: number;
	    steps: CardStep[];
	    notify_on_failure: boolean;
	
	    static createFrom(source: any = {}) {
	        return new LoopConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.interval_seconds = source["interval_seconds"];
	        this.steps = this.convertValues(source["steps"], CardStep);
	        this.notify_on_failure = source["notify_on_failure"];
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
	export class ChatResponse {
	    id: number;
	    turn_id: number;
	    provider: string;
	    status: string;
	    content?: string;
	    error?: string;
	    activity?: string;
	
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
	        this.activity = source["activity"];
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
	export class Conversation {
	    id: number;
	    title: string;
	    kind: string;
	    providers: string[];
	    // Go type: time
	    created_at: any;
	    turns: ChatTurn[];
	    project_path?: string;
	    access?: string;
	    loop: LoopConfig;
	    loop_running: boolean;
	    runs?: CardRun[];
	    role?: string;
	    dialogue_state?: string;
	
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
	        this.turns = this.convertValues(source["turns"], ChatTurn);
	        this.project_path = source["project_path"];
	        this.access = source["access"];
	        this.loop = this.convertValues(source["loop"], LoopConfig);
	        this.loop_running = source["loop_running"];
	        this.runs = this.convertValues(source["runs"], CardRun);
	        this.role = source["role"];
	        this.dialogue_state = source["dialogue_state"];
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
	    links: CanvasLink[];
	
	    static createFrom(source: any = {}) {
	        return new Canvas(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conversations = this.convertValues(source["conversations"], Conversation);
	        this.nodes = this.convertValues(source["nodes"], CanvasNode);
	        this.links = this.convertValues(source["links"], CanvasLink);
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
	
	
	
	
	
	
	export class NewConversation {
	    title: string;
	    kind: string;
	    providers: string[];
	    project_path?: string;
	    access?: string;
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
	        this.project_path = source["project_path"];
	        this.access = source["access"];
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
	export class Quota {
	    short_label?: string;
	    short_utilization: number;
	    short_resets_at?: number;
	    long_label?: string;
	    long_utilization: number;
	    long_resets_at?: number;
	
	    static createFrom(source: any = {}) {
	        return new Quota(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.short_label = source["short_label"];
	        this.short_utilization = source["short_utilization"];
	        this.short_resets_at = source["short_resets_at"];
	        this.long_label = source["long_label"];
	        this.long_utilization = source["long_utilization"];
	        this.long_resets_at = source["long_resets_at"];
	    }
	}
	export class Provider {
	    name: string;
	    kind: string;
	    available: boolean;
	    command?: string;
	    quota?: Quota;
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.available = source["available"];
	        this.command = source["command"];
	        this.quota = this.convertValues(source["quota"], Quota);
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
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.healthy = source["healthy"];
	        this.version = source["version"];
	        this.providers = this.convertValues(source["providers"], Provider);
	        this.runs = this.convertValues(source["runs"], Run);
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

export namespace vcs {
	
	export class Change {
	    path: string;
	    status: string;
	    staged: boolean;
	    untracked: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Change(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.status = source["status"];
	        this.staged = source["staged"];
	        this.untracked = source["untracked"];
	    }
	}
	export class Diff {
	    path: string;
	    patch: string;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Diff(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.patch = source["patch"];
	        this.truncated = source["truncated"];
	    }
	}
	export class Status {
	    project: string;
	    branch?: string;
	    changes: Change[];
	    available: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.branch = source["branch"];
	        this.changes = this.convertValues(source["changes"], Change);
	        this.available = source["available"];
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

