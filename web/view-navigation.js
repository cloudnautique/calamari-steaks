export const viewTool={name:'select_view',description:'Select Terminal, Files, or Desktop in this existing widget. Does not take keyboard focus or type input. Refuses while the person is using the widget.',inputSchema:{type:'object',properties:{boxId:{type:'string'},view:{type:'string',enum:['terminal','files','desktop']},filePath:{type:'string',description:'Optional absolute file path for the Files preview.'}},required:['boxId','view'],additionalProperties:false}};
export function viewNavigation(args,currentBox,busy){
 if(busy)throw Error('The person is using this widget. View selection was left unchanged; wait until they leave the input.');
 if(!currentBox||args?.boxId!==currentBox)throw Error('This widget is showing another box. Use its exact active box ID.');
 if(!['terminal','files','desktop'].includes(args.view))throw Error('Choose terminal, files, or desktop.');
 if(args.filePath&&(typeof args.filePath!=='string'||args.view!=='files'||!args.filePath.startsWith('/')||args.filePath.includes('\0')||args.filePath.includes('\\')||args.filePath.length>4096))throw Error('A Files preview needs an absolute file path in the current box.');
 return {boxId:currentBox,view:args.view,filePath:args.filePath};
}
