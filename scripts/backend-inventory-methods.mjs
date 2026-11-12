// Keep unresolved computed calls visible instead of dropping them or inventing
// an HTTP method. Conditional frontend create/edit calls yield both contracts.
export function requestMethods(member) {
 const names=node=>node?.type==='StringLiteral'?[node.value]:node?.type==='ConditionalExpression'?[...names(node.consequent),...names(node.alternate)]:['DYNAMIC'];
 const candidates=member.computed?names(member.property):[member.property?.name??'DYNAMIC'];
 const verbs={get:'GET',post:'POST',put:'PUT',patch:'PATCH',delete:'DELETE',postForm:'POST',postStream:'POST'};
 return [...new Set(candidates)].map(name=>({name,method:verbs[name]??'DYNAMIC',encoding:name==='postForm'?'form':name==='postStream'?'event-stream':undefined}));
}
