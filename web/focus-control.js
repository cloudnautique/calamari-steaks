// All ownership changes and user submissions share one queue. Losing focus
// yields after already queued user input, never in the middle of a submission.
export class FocusControl {
  constructor(acquire, release) { this.acquire=acquire; this.release=release; this.held=false; this.focused=false; this.queue=Promise.resolve(); }
  enqueue(action) {
    const result=this.queue.then(action);
    this.queue=result.catch(()=>{});
    return result;
  }
  focus(value) {
    this.focused=value;
    return this.enqueue(async()=>{
      if(this.focused&&!this.held) { await this.acquire(); this.held=true; }
      if(!this.focused&&this.held) { await this.release(); this.held=false; }
    });
  }
  run(action) {
    return this.enqueue(async()=>{
      if(!this.held) { await this.acquire(); this.held=true; }
      try { return await action(); }
      finally { if(!this.focused&&this.held) { await this.release(); this.held=false; } }
    });
  }
}
