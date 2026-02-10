@echo off
set DO_AI_IDLE=5s
set DO_AI_DEBUG=1
echo Starting do-ai test > C:\Users\Administrator\Desktop\do-ai-test.log
echo MODE: DEFAULT (enter) >> C:\Users\Administrator\Desktop\do-ai-test.log
C:\Users\Administrator\Desktop\do-ai.exe claude >> C:\Users\Administrator\Desktop\do-ai-test.log 2>&1
