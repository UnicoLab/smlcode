## Improve GUI

GUI is a nice start but let's make it much more professional and user friendly:

- improve the styling of the gui, to make it more professional and good looking not with some strate colour codes like currently

- main live stream needs big imrpovement on rendering we should see nice icons representing dedicated agents with animations and seeing what they are actually doing, status, live updates etc ... succeeded or failed etc ...

- Maybe some kind of a visuals on how agents collaborated like the nice display of states progressbas we have on top is nice but can be drastically imrpoved !

- markdown display should be much better for diplay readability and editing etc

- we shoudl see and be able to know what agent or sub-agent or current step recieved and input command, what it did, what it changes etc ... so we have full observability per action, step etc !!!

- when everything is done for a given query we shoudl put it into archives with all the history etc, it should be a separate thread or something so we keep the history per project etc !!! 

-> when executing some request we shoudl have nicely displayed plan and tasks great visually so we can track the overall progress ! Not only using text or markdown files here, but in the GUI !!! For example when a query is given to the slmcode it shoudl first get context, skills, memories all agents and entire context of the project and it's capabilities, plan everything, split into tasks etc ... then generate populate board with these taks with attributes agents etc ... live updates everywhere !!

-> live stream GUI shoudl be much much better visually !!!

-> GUI shoudl adapt to screan size etc, dynamic layout that adapts, resizes etc !

-> I should also always see what is going on somewhere like in the status bar or in the footer, like current active agents working on ... progress per task etc ... performances ... be able to stop each process separetyl etc ... hols or enrich with additional context as well !

-> for all tasks we should also be able to handle their dependencies, we can display it nicely using react-flow somehow like nodes and edges etc ... or some nice method -> and this shoudl be clear for codign agents etc 

-> on the live beedback live scroll to the last even shoudl be enabled by default ! We also shoudl be able to pause the current loop and add context if we want or stop it if something is not right etc ... 

## Task creation

- make sure tasks creation is well integrated and will work correctly for sub-agents and everything -> currently we are creating one big file ... so maybe this is somehow a too big context for smalelr sub-agents -> or the coordinator distributes the task correctly to all agents and then just updates this bigger file . Let's make sure it works perfectly


## Agents

- allow user to create agents based on skills, everything should be editable and customizable by the user easily either using GUI, TUI or just by adding files etc !


## Generic skills, agents, tools and other things

We should be able to have centralized storage of skills, agents, tools and anything else allowing to specify slmcode functionality globall on the system level not only on the project level and it shoudl autodiscover and propage it gloablly !

## Waves

I can see some concept of "## Wave lessons (2026-07-29T15:06:52+02:00)" let's make sure that we keep coherent memories or context per project so onece we do something all further request agents and everything will have already usable context and informations like code et orgnization etc ... 
So we keep rpogressively all the knowledge about the project after each run getting better and better and not being reinitialized form scratch on every run ... but let's make sure that each agent get's correct context and info and when designing new agent we will be able to precisely controll this as well !!!


## Make sure we handle failures in outputs and tasks nicely

Like: **Blocked:** T1: review rejected after max retriesNo output to review for task verification Issues: - No worker output provided for review ... we need to make sure we have correction loops, validation and everything in place to handle these kind of problems, retries or even ask user what to do if necessary !!

'''markdown
## Leasons learned

Maybe using what agent has extracted during tests:
# Long-term Memory

## Lessons




## Wave lessons (2026-07-29T15:06:11+02:00)

- ⚠ Avoid repeating T1 failure: review rejected after max retries

- ⚠ Project directory creation blocked by review rejection after max retries
- ⚙ Implement retry logic with exponential backoff for CI/CD pipeline steps
- ⚠ Review rejection prevents atomic task execution in automated workflows
- ⚙ Add error handling and status checking before directory creation steps
- ⚠ Max retries exceeded without successful project structure creation


## Wave lessons (2026-07-29T15:06:52+02:00)

- ⚠ Avoid repeating T1 failure: review rejected after max retries

- ⚠ Review rejection after max retries indicates either invalid code structure, missing dependencies, or violation of project conventions
- ⚙ LangGraph agent creation should follow standard project structure with dedicated modules for agents, tools, and state management
- ✓ Project directory structure should include clear separation of concerns with dedicated folders for agents, tools, and state definitions
- ⚠ Incomplete code blocks (truncated output) suggest task execution was cut off before proper validation
- ⚙ Agent definitions should be properly structured with clear imports and function signatures matching project conventions


## Wave lessons (2026-07-29T15:07:17+02:00)

- ⚠ Avoid repeating T1 failure: review rejected after max retries

- ⚠ Project directory creation blocked by review rejection after max retries
- ⚙ Implement retry logic with exponential backoff for CI/CD pipeline steps
- ⚠ Review rejection prevents atomic task execution in automated workflows
- ⚙ Add fallback mechanisms for blocked atomic tasks in deployment pipelines
- ⚠ Max retries exceeded during project structure creation


## Wave lessons (2026-07-29T15:08:31+02:00)

- ✓ T2 (Implement state management): {   "status": "done",   "summary": "Fixed formatting issue in state.
- ⚙ Acceptance pattern that worked for T2: Confirm that state.
- ⚙ Human note honored on T2: Begin state management implementation - must be done after directory structure exists
- ⚠ Avoid repeating T4 failure: review rejected after max retries

- ✓ State management fixes require explicit blank lines after imports to pass review checks
- ⚠ Tools module creation failed due to review rejection after max retries - likely missing required file structure or content
- ⚙ Edit tasks must include ws_write or ws_edit evidence to pass review validation
- ⚙ File formatting must match existing patterns exactly to avoid review rejections
- ⚠ Tool module creation blocked by insufficient content or missing required dependencies in tools.py


## Wave lessons (2026-07-29T15:13:15+02:00)

- ✓ T1 (Create project directory structure): {   "status": "done",   "summary": "Fixed the langgraph project by implementing proper langgraph patterns in all core files.
- ⚙ Acceptance pattern that worked for T1: Verify that all four Python files exist in the project root directory with empty content.
- ⚙ Human note honored on T1: Start project directory creation - all core files need to be established first Unblocking T1 by resolving the project directory creation issue.

- ⚠ Review rejection after max retries indicates need for better initial design validation before full implementation
- ⚙ Use async execution patterns in main.py for better langgraph agent handling
- ✓ StateGraph implementation in agents.py successfully connects all core components with proper state management
- ⚙ Implement proper error handling and retry logic for review processes in project workflows
- ⚠ Langgraph patterns must be validated early in development to prevent later review rejections


## Wave lessons (2026-07-29T15:17:00+02:00)

- ⚠ Avoid repeating T3 failure: review rejected after max retries

- ✓ Created initial project structure and dependencies for langgraph agent successfully
- ⚠ Task blocked on state.py file review - rejected after max retries
- ⚙ Use consistent project structure with clear separation of agent components
- ⚙ Implement retry logic with max attempts for file review tasks
- ⚠ State file review failed to complete - need better error handling for file dependencies


## Wave lessons (2026-07-29T15:18:56+02:00)

- ⚠ Avoid repeating T5 failure: review rejected after max retries

- ⚠ T5 task blocked due to review rejection after max retries - indicates strict code quality gates or template mismatches
- ⚙ Python shebang #!/usr/bin/env python3 should be first line in executable scripts
- ✓ Tools module structure with TypedDict and Tool typing follows langgraph conventions
- ⚠ Task completion truncated mid-response suggests CI/CD pipeline interruption or resource limits
- ⚙ langgraph tools require proper Tool type annotation with input/output specifications


## Auto-lessons (2026-07-29T15:20:26+02:00)

- ✓ T2 (Implement state management): {   "status": "done",   "summary": "Fixed formatting issue in state.
- ⚙ Acceptance pattern that worked for T2: Confirm that state.
- ⚙ Human note honored on T2: Begin state management implementation - must be done after directory structure exists
- ⚠ Avoid repeating T3 failure: review rejected after max retries
- ⚠ Avoid repeating T4 failure: review rejected after max retries
- ⚠ Avoid repeating T5 failure: review rejected after max retries
- ✓ T1 (Create project directory structure): {   "status": "done",   "summary": "Fixed the langgraph project by implementing proper langgraph patterns in all core files.
- ⚙ Acceptance pattern that worked for T1: Verify that all four Python files exist in the project root directory with empty content.
- ⚙ Human note honored on T1: Start project directory creation - all core files need to be established first Unblocking T1 by resolving the project directory creation issue.


## Session distillation (2026-07-29T15:21:26+02:00)

- ⚠ Missing actual state schema implementation in state.py (only formatting fix applied)
- ⚠ Tools module lacks functional tool definitions beyond TypedDict structure
- ⚠ Main.py missing core agent workflow orchestration and execution logic
- ⚠ Agents.py incomplete - missing actual agent component implementations
- ⚠ Project directory structure not fully validated with runnable test
- ⚠ State management does not include conversation history tracking
- ⚠ Tools module missing real tool functions with proper error handling
- ⚠ No proper langgraph API usage examples or version compatibility checks


## Wave lessons (2026-07-29T15:28:28+02:00)

- ⚠ Avoid repeating T1 failure: review rejected after max retries

- ⚠ oMLX setup verification failed with 'review rejected after max retries' error - indicates network or authentication issues with local model access
- ⚙ Always verify local model accessibility before proceeding with downstream tasks in ML workflows
- ⚠ Local LM2.5 model setup blocked by retry limit exhaustion - requires manual intervention or network troubleshooting
- ⚙ Implement exponential backoff retry logic for local model access with clear error boundaries
- ⚠ T1 task blocked due to incomplete oMLX environment setup - prevents progress in subsequent atomic tasks


ERROR: review rejected after max retries

REVIEW:
No actionable code changes provided for review
Issues:
- Worker did not provide actual file content or code changes for review
- No concrete implementation or file modifications to verify

Looking at the task requirements and the current code, I need to modify agents.py to use oMLX model for agent execution. The current implementation doesn't actually use any model - it just has basic logic that doesn't involve model inference.
'''

can help fix some problems and improve overall implementation

## Errors logs
Let's make sure that we store all the logs, especially on failure with all the metadata so we can analyze them using LLM and fix all potential problems, inifinite loops, or other functional issues untill it's fully fixed ...

Maybe let's also write a dedicated errors.md file and keep it so what is failing and why with all the context, by default ?

## Testing and critic

Let's imrpove testing and critics loops and this part if very valuable for our implementation, slms can easily be lost in various contexts tasks and instan user feedbacks without keeping the global goals and taks view and coordinating and updating etc ... let's make sure we have this implemented greatly for SLMs with all the features we need to make a great quality code !!! 

## Projec tInfo

I have noticed that the projec tinfo was kep empty during my entire usage of the slmcode ... which is strange -> deep dive and fix all the bugs ! Context if very important !



## Testing results

- on my initial test I can notice lot's of failures -> deep dive and fix all problems 

We need everything to be self evolving and improving all the time !!! 

## Planing

When planing what needs to be done, let's take enough time to consider everytg we need to make everythign right, right context, right deep dive on code or docs or research or everything we need to ask or user to clarify so that we have a full picture. LEt's drastically improve this part as well in context of SLMs !!!



Deep dive on all the TODOs and example project I have put in: 
To fix all current problems and improve everything making it much better, more efficient and production ready !!

## TUI improvements

We need to drastically imrpove the TUI especially if we can't to code in the terminal, currently we display tons of logs and it's not very nice visually for the user .. when displaying run or something our coding TUI shoudl be implemented like the one form claude code ... where everything is much cleaner etc !!! And give visual overviews and status in the termina TUI -> so let's revamp this part drastically cause some of the users will not use the studio but work entirely in the terminal and the experiance shoudl be grat !!! Also take inspiration from claude code how the input is always possible, when agents are working it's a background process attached to a thread but user can still interact with the slmcode etc .. -> make sure we have all the features of claude code as well in the terminal !